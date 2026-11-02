package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/xmhrnx/giode-cli/internal/config"
)

const updateKeyUsage = `
Usage: giode update-key [options]

Creates the Ed25519 key pair that signs update feeds: the private key in
update.key (or the file given with -o), which must stay secret and out of
the repository, and the public key in giode.toml's [update] section,
which builds put into the app. Set update.feed_url to where the feed
will be.
`

const releaseUsage = `
Usage: giode release [options] [target...]

Builds desktop targets and writes an update release to dist/update: the
downloads, feed.json listing them with their SHA-256, and feed.json.sig,
signed with the private key (update.private_key, update.key by default,
or the key itself in $GIODE_UPDATE_KEY). Upload the directory to
update.feed_url's location; apps built with [update] find it with the
giode update package.

Targets are windows (the program), windows-installer (the setup program,
run silently to update), macos (the universal app, zipped), linux (the
program) and appimage; by default this computer's and windows.
`

func cmdUpdateKey(args []string) error {
	fs := flags("update-key", updateKeyUsage)
	dir := fs.String("C", ".", "project directory")
	out := fs.String("o", "update.key", "private key file, relative to the project")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	keyPath := c.Path(*out)
	if _, err := os.Stat(keyPath); err == nil {
		return fmt.Errorf("%s exists; delete it first to make a new key (apps checking the old one won't accept feeds signed with a new one)", relPath(keyPath))
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	tomlPath := filepath.Join(c.Dir, config.FileName)
	if err := setTOMLValue(tomlPath, "update", "public_key", base64.StdEncoding.EncodeToString(pub)); err != nil {
		return err
	}
	if *out != "update.key" {
		if err := setTOMLValue(tomlPath, "update", "private_key", *out); err != nil {
			return err
		}
	}
	addToGitignore(c.Dir, *out)
	fmt.Printf("Created %s (secret: keep it out of the repository; it's in .gitignore) and set update.public_key in giode.toml.\n", relPath(keyPath))
	if c.Update.FeedURL == "" {
		fmt.Println("Set update.feed_url in giode.toml to where you'll upload feed.json, then: giode release")
	}
	return nil
}

// addToGitignore adds a line to the project's .gitignore when it has one
// without it.
func addToGitignore(dir, line string) {
	p := filepath.Join(dir, ".gitignore")
	b, err := os.ReadFile(p)
	if err != nil || strings.Contains("\n"+string(b)+"\n", "\n"+line+"\n") {
		return
	}
	s := strings.TrimRight(string(b), "\n") + "\n" + line + "\n"
	os.WriteFile(p, []byte(s), 0o644)
}

// feed is the update feed (see the giode update package).
type feed struct {
	Version string           `json:"version"`
	Notes   string           `json:"notes,omitempty"`
	Date    string           `json:"date"`
	Assets  map[string]asset `json:"assets"`
}

type asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Kind   string `json:"kind"`
}

func cmdRelease(args []string) error {
	fs := flags("release", releaseUsage)
	dir := fs.String("C", ".", "project directory")
	baseURL := fs.String("base-url", "", "absolute URL prefix for the downloads (default: next to the feed)")
	notes := fs.String("notes", "", "release notes")
	notesFile := fs.String("notes-file", "", "file with the release notes")
	fs.BoolVar(&verbose, "x", false, "print the commands run")
	targets, err := parse(fs, args)
	if err != nil {
		return err
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if c.Update.PublicKey == "" {
		return errors.New("no update.public_key in giode.toml; run giode update-key")
	}
	priv, err := updatePrivateKey(c)
	if err != nil {
		return err
	}
	if *notesFile != "" {
		b, err := os.ReadFile(*notesFile)
		if err != nil {
			return err
		}
		*notes = string(b)
	}
	if len(targets) == 0 {
		targets = []string{hostTarget()}
		if hostTarget() != "windows" {
			targets = append(targets, "windows")
		}
	}
	out, err := outDir(c, "update")
	if err != nil {
		return err
	}
	f := feed{Version: c.App.Version, Notes: strings.TrimSpace(*notes), Date: time.Now().UTC().Format(time.RFC3339), Assets: map[string]asset{}}
	for _, t := range expandTargets(targets) {
		fmt.Printf("Building %s…\n", t)
		start := time.Now()
		built, err := buildTarget(c, t, buildOpts{release: true})
		if err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
		platform, kind, name := "", "", ""
		switch t {
		case "windows", "windows-installer":
			arch := c.Windows.Arch
			if arch == "" {
				arch = "amd64"
			}
			platform, kind, name = "windows-"+arch, "exe", fmt.Sprintf("%s-%s-windows-%s.exe", binaryName(c), c.App.Version, arch)
			if t == "windows-installer" {
				kind, name = "installer", fmt.Sprintf("%s-%s-%s-setup.exe", binaryName(c), c.App.Version, arch)
			}
		case "macos":
			platform, kind, name = "darwin-universal", "app.zip", fmt.Sprintf("%s-%s-macos.zip", binaryName(c), c.App.Version)
		case "linux", "appimage":
			platform, kind, name = "linux-"+runtime.GOARCH, "binary", fmt.Sprintf("%s-%s-linux-%s", strings.ToLower(binaryName(c)), c.App.Version, runtime.GOARCH)
			if t == "appimage" {
				kind, name = "appimage", name+".AppImage"
			}
		default:
			return fmt.Errorf("giode release can't publish %s; targets: windows, windows-installer, macos, linux, appimage", t)
		}
		dst := filepath.Join(out, name)
		if kind == "app.zip" {
			err = zipApp(built, dst)
		} else {
			err = copyFile(built, dst)
		}
		if err != nil {
			return err
		}
		sum, size, err := hashFile(dst)
		if err != nil {
			return err
		}
		url := name
		if *baseURL != "" {
			url = strings.TrimRight(*baseURL, "/") + "/" + name
		}
		f.Assets[platform] = asset{URL: url, SHA256: sum, Size: size, Kind: kind}
		fmt.Printf("  %s (%s)\n", relPath(dst), since(start))
	}
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body))
	if err := os.WriteFile(filepath.Join(out, "feed.json"), body, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "feed.json.sig"), []byte(sig+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote %s and feed.json.sig; upload %s to ", relPath(filepath.Join(out, "feed.json")), relPath(out))
	if c.Update.FeedURL != "" {
		fmt.Println(strings.TrimSuffix(c.Update.FeedURL, "feed.json"))
	} else {
		fmt.Println("the URL in update.feed_url")
	}
	return nil
}

// updatePrivateKey reads the private key: $GIODE_UPDATE_KEY, or the
// key file.
func updatePrivateKey(c *config.Config) (ed25519.PrivateKey, error) {
	s := os.Getenv("GIODE_UPDATE_KEY")
	if s == "" {
		path := c.Update.PrivateKey
		if path == "" {
			path = "update.key"
		}
		b, err := os.ReadFile(c.Path(path))
		if err != nil {
			return nil, fmt.Errorf("no private key: %w (run giode update-key, or set $GIODE_UPDATE_KEY)", err)
		}
		s = string(b)
	}
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(k) != ed25519.PrivateKeySize {
		return nil, errors.New("the update private key isn't a base64 Ed25519 key")
	}
	priv := ed25519.PrivateKey(k)
	if base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)) != strings.TrimSpace(c.Update.PublicKey) {
		return nil, errors.New("the update private key doesn't match update.public_key in giode.toml")
	}
	return priv, nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// zipApp zips an .app bundle, keeping permissions and symlinks.
func zipApp(app, dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	base := filepath.Dir(app)
	err = filepath.Walk(app, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			h.Name += "/"
			_, err := zw.CreateHeader(h)
			return err
		}
		h.Method = zip.Deflate
		if info.Mode()&os.ModeSymlink != 0 {
			h.Method = zip.Store
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			w, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			_, err = io.WriteString(w, link)
			return err
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

// zipTree zips the files under root, named relative to it.
func zipTree(root, dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}
