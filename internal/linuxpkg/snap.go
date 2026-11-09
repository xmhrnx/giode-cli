package linuxpkg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// SnapOptions configures a snap.
type SnapOptions struct {
	// Name is the snap's name in the Snap Store: lower case letters,
	// digits and hyphens.
	Name string
	// Plugs are the interfaces the app connects to.
	Plugs []string
	// Grade is "stable" or "devel".
	Grade string
}

// DefaultSnapPlugs let a Gio app show windows, use the GPU and the
// network, and play sound.
var DefaultSnapPlugs = []string{"desktop", "desktop-legacy", "wayland", "x11", "opengl", "network", "audio-playback"}

var snapNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// SnapName makes a snap name from an app name: lower case, with
// anything but letters and digits as hyphens.
func SnapName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// snapArch maps GOARCH to snap architectures.
var snapArch = map[string]string{"amd64": "amd64", "arm64": "arm64", "arm": "armhf", "riscv64": "riscv64", "ppc64le": "ppc64el", "s390x": "s390x"}

// Snap writes a snapcraft project into dir packing the built app: the
// files under root/, snap/snapcraft.yaml (strict confinement, core24,
// the GNOME extension for the desktop libraries), and the desktop entry
// and icon in snap/gui. It packs it into out with snapcraft when that's
// installed, and reports whether it did.
func Snap(a App, o SnapOptions, dir, out string, verbose bool) (bool, error) {
	if o.Name == "" {
		o.Name = SnapName(a.Name)
	}
	if !snapNameRE.MatchString(o.Name) || len(o.Name) > 40 {
		return false, fmt.Errorf("the snap name %q must be lower case letters, digits and hyphens, at most 40 (linux.snap.name)", o.Name)
	}
	arch, ok := snapArch[a.Arch]
	if !ok {
		return false, fmt.Errorf("snaps don't support %s", a.Arch)
	}
	if o.Grade == "" {
		o.Grade = "stable"
	}
	plugs := o.Plugs
	if len(plugs) == 0 {
		plugs = DefaultSnapPlugs
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	files, err := Files(a, "")
	if err != nil {
		return false, err
	}
	for _, f := range files {
		p := filepath.Join(dir, "root", filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return false, err
		}
		if err := os.WriteFile(p, f.Body, f.Mode); err != nil {
			return false, err
		}
	}
	// snapcraft takes the app's desktop entry and icon from snap/gui,
	// named after the app and the snap.
	gui := filepath.Join(dir, "snap", "gui")
	if err := os.MkdirAll(gui, 0o755); err != nil {
		return false, err
	}
	desktop := DesktopEntry(a)
	desktop = strings.Replace(desktop, "Exec="+a.Command, "Exec="+o.Name, 1)
	desktop = regexp.MustCompile(`(?m)^Icon=.*$`).ReplaceAllLiteralString(desktop, "Icon=${SNAP}/meta/gui/"+o.Name+".png")
	if err := os.WriteFile(filepath.Join(gui, o.Name+".desktop"), []byte(desktop), 0o644); err != nil {
		return false, err
	}
	if a.Icon != "" {
		icons, err := Icons(a.Icon)
		if err != nil {
			return false, fmt.Errorf("icon: %w", err)
		}
		if err := os.WriteFile(filepath.Join(gui, o.Name+".png"), icons[512], 0o644); err != nil {
			return false, err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "snap", "snapcraft.yaml"), []byte(SnapcraftYAML(a, o, arch, plugs)), 0o644); err != nil {
		return false, err
	}
	snapcraft, err := exec.LookPath("snapcraft")
	if err != nil {
		return false, nil
	}
	cmd := exec.Command(snapcraft, "pack", "--output", out)
	cmd.Dir = dir
	if verbose {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return true, cmd.Run()
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("snapcraft pack: %v\n%s", err, b)
	}
	return true, nil
}

// SnapcraftYAML is the snap's snapcraft.yaml.
func SnapcraftYAML(a App, o SnapOptions, arch string, plugs []string) string {
	summary := a.Summary
	if summary == "" {
		summary = a.Name
	}
	if r := []rune(summary); len(r) > 78 {
		summary = string(r[:77]) + "…"
	}
	desc := a.Description
	if desc == "" {
		desc = summary
	}
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\n", o.Name)
	fmt.Fprintf(&b, "title: %s\n", yamlString(a.Name))
	fmt.Fprintf(&b, "base: core24\n")
	fmt.Fprintf(&b, "version: %s\n", yamlString(a.Version))
	fmt.Fprintf(&b, "summary: %s\n", yamlString(summary))
	b.WriteString("description: |\n")
	for _, l := range strings.Split(desc, "\n") {
		b.WriteString("  " + l + "\n")
	}
	fmt.Fprintf(&b, "grade: %s\n", o.Grade)
	b.WriteString("confinement: strict\n")
	if a.License != "" {
		fmt.Fprintf(&b, "license: %s\n", yamlString(a.License))
	}
	fmt.Fprintf(&b, "platforms:\n  %s:\n    build-on: [%s]\n    build-for: [%s]\n", arch, arch, arch)
	fmt.Fprintf(&b, "apps:\n  %s:\n    command: bin/%s\n    common-id: %s\n    extensions: [gnome]\n    plugs:\n", o.Name, a.Command, a.ID)
	for _, p := range plugs {
		fmt.Fprintf(&b, "      - %s\n", p)
	}
	fmt.Fprintf(&b, "parts:\n  %s:\n    plugin: dump\n    source: root\n", o.Name)
	return b.String()
}

// yamlString quotes s as a YAML double-quoted string.
func yamlString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}
