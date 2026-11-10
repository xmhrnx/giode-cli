package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/xmhrnx/giode-cli/internal/config"
	"github.com/xmhrnx/giode-cli/internal/giopatch"
	"github.com/xmhrnx/giode-cli/internal/linuxpkg"
	"github.com/xmhrnx/giode-cli/internal/progress"
)

// goProxy is where Flathub's builders download the Go modules from.
const goProxy = "https://proxy.golang.org"

// flatpakSource is a flatpak-builder source.
type flatpakSource struct {
	Type            string `json:"type"`
	URL             string `json:"url,omitempty"`
	Tag             string `json:"tag,omitempty"`
	Commit          string `json:"commit,omitempty"`
	Path            string `json:"path,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
	Contents        string `json:"contents,omitempty"`
	Dest            string `json:"dest,omitempty"`
	DestFilename    string `json:"dest-filename,omitempty"`
	StripComponents *int   `json:"strip-components,omitempty"`
}

type flatpakModule struct {
	Name          string          `json:"name"`
	Buildsystem   string          `json:"buildsystem"`
	BuildOptions  map[string]any  `json:"build-options,omitempty"`
	BuildCommands []string        `json:"build-commands"`
	Sources       []flatpakSource `json:"sources"`
}

type flatpakManifest struct {
	ID             string          `json:"id"`
	Runtime        string          `json:"runtime"`
	RuntimeVersion string          `json:"runtime-version"`
	SDK            string          `json:"sdk"`
	SDKExtensions  []string        `json:"sdk-extensions"`
	Command        string          `json:"command"`
	FinishArgs     []string        `json:"finish-args"`
	Modules        []flatpakModule `json:"modules"`
}

// buildFlathub writes a Flathub submission: a manifest building the app
// from its git tag, offline, with Go from the SDK extension and every Go
// module as a source, Gio's patches as a patch file, and the desktop
// entry, AppStream file and icons beside it.
func buildFlathub(c *config.Config, o buildOpts) (string, error) {
	d, err := outDir(c, "linux")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(d, "flathub")
	if o.output != "" {
		dir, _ = filepath.Abs(o.output)
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if len(c.Native.Linux.Libraries) > 0 || len(c.Native.Linux.Search) > 0 {
		progress.Logf("native.linux libraries aren't built from source for Flathub: add them as modules before the app's in the manifest")
	}
	if len(c.Linux.Screenshots) == 0 {
		progress.Logf("Flathub requires screenshots: set linux.screenshots")
	}

	git, err := gitSource(c)
	if err != nil {
		return "", err
	}
	modDir, err := goEnv(c.Dir, "GOMOD")
	if err != nil {
		return "", err
	}
	if modDir == "" || modDir == os.DevNull {
		return "", errors.New("Flathub builds need a Go module (go.mod)")
	}
	modDir = filepath.Dir(modDir)

	// Gio's patches, as Flathub takes changes to upstream code.
	patched := c.App.GioPatches == nil || *c.App.GioPatches
	var gioVersion string
	if patched {
		done := progress.Begin("Diffing giode's Gio patches")
		var diff string
		diff, gioVersion, err = giopatch.Diff(c.Dir, giopatch.All)
		done(err)
		if err != nil {
			return "", fmt.Errorf("Gio's patches: %w (set app.gio_patches = false to build against plain Gio)", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "gio-giode.patch"), []byte(diff), 0o644); err != nil {
			return "", err
		}
	}

	done := progress.Begin("Hashing the Go modules")
	modSources, err := goModuleSources(modDir, patched)
	done(err)
	if err != nil {
		return "", err
	}

	// The desktop entry, AppStream file, icons and file types.
	a := linuxApp(c, "", o.arch)
	a.Command = strings.ToLower(binaryName(c))
	files, err := linuxpkg.Files(a, "/app")
	if err != nil {
		return "", err
	}
	var installs []string
	var sources []flatpakSource
	sources = append(sources, git)
	for _, f := range files {
		name := localName(f.Path)
		if err := os.WriteFile(filepath.Join(dir, name), f.Body, 0o644); err != nil {
			return "", err
		}
		sources = append(sources, flatpakSource{Type: "file", Path: name})
		installs = append(installs, fmt.Sprintf("install -Dm%o %s %s", f.Mode, name, f.Path))
	}
	if patched {
		one := 1
		sources = append(sources,
			flatpakSource{Type: "archive", URL: goProxy + "/gioui.org/@v/" + escapeModule(gioVersion) + ".zip", SHA256: modSHA(modSources, "gioui.org", gioVersion), Dest: "gio", StripComponents: &one},
			flatpakSource{Type: "patch", Path: "gio-giode.patch", Dest: "gio"},
		)
	}
	for _, s := range modSources {
		if s.Type != "gio-zip" {
			sources = append(sources, s)
		}
	}

	// The build: the app's directory in the checkout, offline, against
	// the module proxy the sources fill.
	rel, err := gitRelDir(c.Dir)
	if err != nil {
		return "", err
	}
	tags := c.App.Tags
	if patched {
		tags = addTag(addTag(addTag(tags, giopatch.Tag), giopatch.Tag2), giopatch.Tag3)
	}
	ldflags := strings.TrimSpace("-s -w -X gioui.org/app.ID=" + c.LinuxID() + " " + desktopLdflags(c, "linux"))
	var b strings.Builder
	b.WriteString("root=$PWD; ")
	if rel != "." {
		fmt.Fprintf(&b, "cd %s; ", shellQuote(rel))
	}
	b.WriteString("export GOPROXY=file://$root/goproxy GOSUMDB=off GOTOOLCHAIN=local GOFLAGS='-mod=mod -modcacherw' GOPATH=$root/.gopath GOCACHE=$root/.gocache CGO_ENABLED=1; ")
	if patched {
		b.WriteString("go mod edit -replace gioui.org=$root/gio; ")
	}
	fmt.Fprintf(&b, "go build -trimpath")
	if tags != "" {
		fmt.Fprintf(&b, " -tags %s", shellQuote(tags))
	}
	fmt.Fprintf(&b, " -ldflags %s -o /app/bin/%s %s", shellQuote(ldflags), a.Command, pkgPath(c))
	commands := append([]string{"set -e; " + b.String()}, installs...)

	m := flatpakManifest{
		ID: c.LinuxID(), Runtime: "org.freedesktop.Platform", RuntimeVersion: linuxpkg.FlatpakRuntime,
		SDK: "org.freedesktop.Sdk", SDKExtensions: []string{"org.freedesktop.Sdk.Extension.golang"},
		Command: a.Command, FinishArgs: linuxpkg.FlatpakPermissions(a),
		Modules: []flatpakModule{{
			Name: a.Command, Buildsystem: "simple",
			BuildOptions:  map[string]any{"append-path": "/usr/lib/sdk/golang/bin"},
			BuildCommands: commands, Sources: sources,
		}},
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	mf := filepath.Join(dir, c.LinuxID()+".json")
	if err := os.WriteFile(mf, append(out, '\n'), 0o644); err != nil {
		return "", err
	}
	progress.Logf("Flathub submission in %s: build it with flatpak-builder --user --install-deps-from=flathub --force-clean build %s", relPath(dir), filepath.Base(mf))
	return mf, nil
}

// localName is the name of an installed file beside the manifest: its
// own, with the size added for the icons (one per size).
func localName(p string) string {
	name := path.Base(p)
	if _, rest, ok := strings.Cut(p, "/icons/hicolor/"); ok {
		size, _, _ := strings.Cut(rest, "/")
		ext := path.Ext(name)
		return strings.TrimSuffix(name, ext) + "-" + size + ext
	}
	return name
}

// gitSource is the app's repository at its release tag: the tag on the
// checked out commit, or v<version>.<build>, v<version> or <version>.
func gitSource(c *config.Config) (flatpakSource, error) {
	url, err := gitOutput(c.Dir, "remote", "get-url", "origin")
	if err != nil {
		return flatpakSource{}, fmt.Errorf("Flathub builds from the app's git repository: %w", err)
	}
	url = httpsRepo(url)
	var tag string
	if out, err := gitOutput(c.Dir, "tag", "--points-at", "HEAD"); err == nil && out != "" {
		tag = strings.Fields(out)[0]
	}
	if tag == "" {
		for _, t := range []string{fmt.Sprintf("v%s.%d", c.App.Version, c.App.Build), "v" + c.App.Version, c.App.Version} {
			if _, err := gitOutput(c.Dir, "rev-parse", "-q", "--verify", "refs/tags/"+t); err == nil {
				tag = t
				break
			}
		}
	}
	if tag == "" {
		return flatpakSource{}, fmt.Errorf("Flathub builds from a git tag, and there's none for %s: tag the release (v%s) first", c.App.Version, c.App.Version)
	}
	commit, err := gitOutput(c.Dir, "rev-parse", tag+"^{commit}")
	if err != nil {
		return flatpakSource{}, err
	}
	return flatpakSource{Type: "git", URL: url, Tag: tag, Commit: commit}, nil
}

// httpsRepo turns an SSH remote (git@github.com:me/app.git) into its
// https URL, which Flathub's builders can clone.
func httpsRepo(u string) string {
	if rest, ok := strings.CutPrefix(u, "git@"); ok {
		host, p, _ := strings.Cut(rest, ":")
		return "https://" + host + "/" + p
	}
	return strings.Replace(u, "ssh://git@", "https://", 1)
}

func gitRelDir(dir string) (string, error) {
	top, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	top, err = filepath.EvalSymlinks(top)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(top, abs)
	return filepath.ToSlash(rel), err
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func goEnv(dir, name string) (string, error) {
	cmd := exec.Command("go", "env", name)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// shellQuote quotes s for sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// escapeModule escapes a module path or version for the module proxy:
// upper case letters become ! and the lower case letter.
func escapeModule(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsUpper(r) {
			b.WriteByte('!')
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// goModuleSources lists every file of go.sum as a source in a module
// proxy directory (goproxy/), so go build finds them offline: each
// module's .mod, and the .zip of those with code. Gio's zip is left out
// when the build patches it (it's unpacked separately).
func goModuleSources(modDir string, skipGio bool) ([]flatpakSource, error) {
	f, err := os.Open(filepath.Join(modDir, "go.sum"))
	if err != nil {
		return nil, fmt.Errorf("Flathub builds need go.sum: %w", err)
	}
	defer f.Close()
	type entry struct{ mod, ver, ext string }
	seen := map[entry]bool{}
	var entries []entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 3 {
			continue
		}
		e := entry{mod: fields[0], ver: fields[1], ext: ".zip"}
		if v, ok := strings.CutSuffix(e.ver, "/go.mod"); ok {
			e.ver, e.ext = v, ".mod"
		}
		if !seen[e] {
			seen[e] = true
			entries = append(entries, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.mod != b.mod {
			return a.mod < b.mod
		}
		if a.ver != b.ver {
			return a.ver < b.ver
		}
		return a.ext < b.ext
	})
	cache, _ := goEnv(modDir, "GOMODCACHE")
	sums := make([]string, len(entries))
	errs := make([]error, len(entries))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, e := range entries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rel := escapeModule(e.mod) + "/@v/" + escapeModule(e.ver) + e.ext
			sums[i], errs[i] = moduleFileSHA(filepath.Join(cache, "cache", "download", filepath.FromSlash(rel)), goProxy+"/"+rel)
		}()
	}
	wg.Wait()
	var out []flatpakSource
	for i, e := range entries {
		if errs[i] != nil {
			return nil, fmt.Errorf("%s@%s%s: %w", e.mod, e.ver, e.ext, errs[i])
		}
		dest := "goproxy/" + escapeModule(e.mod) + "/@v"
		file := escapeModule(e.ver) + e.ext
		s := flatpakSource{Type: "file", URL: goProxy + "/" + escapeModule(e.mod) + "/@v/" + file, SHA256: sums[i], Dest: dest, DestFilename: file}
		if skipGio && e.mod == "gioui.org" && e.ext == ".zip" {
			// Kept for its hash: the patched Gio is unpacked from it.
			s.Type = "gio-zip"
		}
		out = append(out, s)
		if e.ext == ".zip" && s.Type == "file" {
			out = append(out, flatpakSource{Type: "inline", Contents: fmt.Sprintf(`{"Version":%q}`, e.ver), Dest: dest, DestFilename: escapeModule(e.ver) + ".info"})
		}
	}
	return out, nil
}

// modSHA returns the hash of a module's zip from sources, removing its
// placeholder.
func modSHA(sources []flatpakSource, mod, ver string) string {
	for _, s := range sources {
		if s.Type == "gio-zip" && strings.Contains(s.URL, "/"+escapeModule(mod)+"/@v/"+escapeModule(ver)+".zip") {
			return s.SHA256
		}
	}
	return ""
}

// moduleFileSHA hashes a module proxy file: the module cache's copy, or
// the proxy's.
func moduleFileSHA(local, url string) (string, error) {
	h := sha256.New()
	if f, err := os.Open(local); err == nil {
		defer f.Close()
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	if _, err := io.Copy(h, resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
