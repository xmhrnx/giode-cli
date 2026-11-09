// Package linuxpkg packages a built Linux app: Debian and RPM packages,
// AppImages and Flatpak manifests, with the desktop entry, icons and
// AppStream metadata desktops and software centers read.
package linuxpkg

import (
	"bytes"
	"fmt"
	"html"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "image/jpeg"

	"golang.org/x/image/draw"
)

// App describes the app to package.
type App struct {
	// Name is the display name, ID the reverse-DNS app ID, Version
	// major.minor.patch and Build the build number.
	Name, ID, Version string
	Build             int
	// Summary is a one-line description; Description a longer one.
	Summary, Description string
	// Publisher makes the app; Maintainer is "Name <email>" for package
	// metadata (the publisher when empty).
	Publisher, Maintainer string
	// Website is the home page, License an SPDX expression.
	Website, License string
	// Categories are freedesktop menu categories.
	Categories []string
	// Binary is the built executable, installed as Command.
	Binary, Command string
	// Icon is a square PNG or JPEG (optional).
	Icon string
	// Arch is the GOARCH the binary was built for.
	Arch string
	// FlatpakPermissions are Flatpak finish-args; a desktop app's
	// defaults (display, GPU, network, sound) when empty.
	FlatpakPermissions []string
	// Schemes are URL schemes the app opens.
	Schemes []string
	// Libs are shared libraries bundled in a private directory,
	// <prefix>/lib/<command>, which the binary's run path includes.
	Libs []string
	// Depends are more Debian packages the app needs; Requires more RPM
	// requirements (sonames, "libmpv.so.2()(64bit)").
	Depends, Requires []string
	// MIMETypes are file types the app opens ("Open with"); MIMEXML a
	// shared-mime-info package declaring the ones only it knows.
	MIMETypes []string
	MIMEXML   string
	// Screenshots are AppStream screenshots, the first the main one.
	Screenshots []Screenshot
}

// Screenshot is an image URL with an optional caption.
type Screenshot struct {
	URL, Caption string
}

// LibDir is where a package's bundled libraries go, below its prefix
// (/usr, /app, an AppDir's usr).
func LibDir(command string) string { return "lib/" + command }

// File is a file to install, by absolute path.
type File struct {
	Path string
	Body []byte
	Mode os.FileMode
}

// iconSizes are the hicolor sizes written.
var iconSizes = []int{16, 32, 48, 64, 128, 256, 512}

// Files returns the files the app installs under prefix (/usr, or /app
// for Flatpak): the binary (when Binary is set), desktop entry, icons
// and AppStream metadata.
func Files(a App, prefix string) ([]File, error) {
	var files []File
	// Without a binary (a Flathub build compiles it), the rest.
	if a.Binary != "" {
		bin, err := os.ReadFile(a.Binary)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: prefix + "/bin/" + a.Command, Body: bin, Mode: 0o755})
	}
	files = append(files,
		File{Path: prefix + "/share/applications/" + a.ID + ".desktop", Body: []byte(DesktopEntry(a)), Mode: 0o644},
		File{Path: prefix + "/share/metainfo/" + a.ID + ".metainfo.xml", Body: []byte(Metainfo(a)), Mode: 0o644},
	)
	if a.MIMEXML != "" {
		files = append(files, File{Path: prefix + "/share/mime/packages/" + a.ID + ".xml", Body: []byte(a.MIMEXML), Mode: 0o644})
	}
	for _, l := range a.Libs {
		b, err := os.ReadFile(l)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: prefix + "/" + LibDir(a.Command) + "/" + filepath.Base(l), Body: b, Mode: 0o644})
	}
	if a.Icon != "" {
		icons, err := Icons(a.Icon)
		if err != nil {
			return nil, fmt.Errorf("icon: %w", err)
		}
		for _, s := range iconSizes {
			files = append(files, File{Path: fmt.Sprintf("%s/share/icons/hicolor/%dx%d/apps/%s.png", prefix, s, s, a.ID), Body: icons[s], Mode: 0o644})
		}
	}
	return files, nil
}

// Icons renders the icon at the hicolor sizes, as PNG.
func Icons(path string) (map[int][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	out := map[int][]byte{}
	for _, s := range iconSizes {
		dst := image.NewNRGBA(image.Rect(0, 0, s, s))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
		var b bytes.Buffer
		if err := png.Encode(&b, dst); err != nil {
			return nil, err
		}
		out[s] = b.Bytes()
	}
	return out, nil
}

// DesktopEntry returns the app's .desktop file.
func DesktopEntry(a App) string {
	cats := "Utility;"
	if len(a.Categories) > 0 {
		cats = strings.Join(a.Categories, ";") + ";"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[Desktop Entry]\nType=Application\nName=%s\n", a.Name)
	if a.Summary != "" {
		fmt.Fprintf(&b, "Comment=%s\n", a.Summary)
	}
	fmt.Fprintf(&b, "Exec=%s %%U\nIcon=%s\nCategories=%s\nTerminal=false\nStartupWMClass=%s\n", a.Command, a.ID, cats, a.Command)
	types := append([]string{}, a.MIMETypes...)
	for _, s := range a.Schemes {
		types = append(types, "x-scheme-handler/"+s)
	}
	if len(types) > 0 {
		fmt.Fprintf(&b, "MimeType=%s;\n", strings.Join(types, ";"))
	}
	return b.String()
}

// Metainfo returns the app's AppStream metadata, which software centers
// (GNOME Software, KDE Discover) and Flathub show.
func Metainfo(a App) string {
	e := html.EscapeString
	license := a.License
	if license == "" {
		license = "LicenseRef-proprietary"
	}
	summary := a.Summary
	if summary == "" {
		summary = a.Name
	}
	desc := a.Description
	if desc == "" {
		desc = summary
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<component type="desktop-application">` + "\n")
	fmt.Fprintf(&b, "  <id>%s</id>\n  <name>%s</name>\n  <summary>%s</summary>\n", e(a.ID), e(a.Name), e(summary))
	fmt.Fprintf(&b, "  <metadata_license>CC0-1.0</metadata_license>\n  <project_license>%s</project_license>\n", e(license))
	if a.Publisher != "" {
		fmt.Fprintf(&b, "  <developer id=\"%s\">\n    <name>%s</name>\n  </developer>\n", e(a.ID), e(a.Publisher))
	}
	fmt.Fprintf(&b, "  <description>\n    <p>%s</p>\n  </description>\n", e(desc))
	fmt.Fprintf(&b, "  <launchable type=\"desktop-id\">%s.desktop</launchable>\n", e(a.ID))
	if a.Website != "" {
		fmt.Fprintf(&b, "  <url type=\"homepage\">%s</url>\n", e(a.Website))
	}
	if len(a.Screenshots) > 0 {
		b.WriteString("  <screenshots>\n")
		for i, sc := range a.Screenshots {
			kind := ""
			if i == 0 {
				kind = ` type="default"`
			}
			fmt.Fprintf(&b, "    <screenshot%s>\n      <image>%s</image>\n", kind, e(sc.URL))
			if sc.Caption != "" {
				fmt.Fprintf(&b, "      <caption>%s</caption>\n", e(sc.Caption))
			}
			b.WriteString("    </screenshot>\n")
		}
		b.WriteString("  </screenshots>\n")
	}
	fmt.Fprintf(&b, "  <releases>\n    <release version=\"%s\" date=\"%s\"/>\n  </releases>\n", e(a.Version), time.Now().UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "  <content_rating type=\"oars-1.1\"/>\n")
	b.WriteString("</component>\n")
	return b.String()
}

// packageName is a Debian/RPM package name for the command.
func packageName(cmd string) string {
	n := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '-', r == '.':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return '-'
	}, cmd)
	if n == "" || !(n[0] >= 'a' && n[0] <= 'z' || n[0] >= '0' && n[0] <= '9') {
		n = "app" + n
	}
	return n
}

func (a App) maintainer() string {
	switch {
	case a.Maintainer != "":
		return a.Maintainer
	case a.Publisher != "":
		return a.Publisher + " <noreply@" + reverseDomain(a.ID) + ">"
	}
	return "Unknown <noreply@" + reverseDomain(a.ID) + ">"
}

// reverseDomain turns com.example.app into example.com.
func reverseDomain(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) < 2 {
		return "localhost"
	}
	return parts[1] + "." + parts[0]
}

// sortFiles orders files by path, for reproducible archives.
func sortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

// dirsOf returns the directories holding files, parents first.
func dirsOf(files []File) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, f := range files {
		parts := strings.Split(strings.TrimPrefix(f.Path, "/"), "/")
		for i := 1; i < len(parts); i++ {
			d := "/" + strings.Join(parts[:i], "/")
			if !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
		}
	}
	sort.Strings(dirs)
	return dirs
}
