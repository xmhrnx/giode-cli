package linuxpkg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// appImageArch maps GOARCH to AppImage architectures.
var appImageArch = map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686", "arm": "armhf"}

// AppDir lays the app out as an AppImage AppDir in dir: usr/ with the
// files, and the desktop entry, icon and AppRun at the top.
func AppDir(a App, dir string) error {
	files, err := Files(a, "/usr")
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, f.Body, f.Mode); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, a.ID+".desktop"), []byte(DesktopEntry(a)), 0o644); err != nil {
		return err
	}
	if a.Icon != "" {
		icon := filepath.Join(dir, "usr/share/icons/hicolor/256x256/apps", a.ID+".png")
		b, err := os.ReadFile(icon)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, a.ID+".png"), b, 0o644); err != nil {
			return err
		}
		os.Symlink(a.ID+".png", filepath.Join(dir, ".DirIcon"))
	}
	run := "#!/bin/sh\nHERE=\"$(dirname \"$(readlink -f \"$0\")\")\"\nexec \"$HERE/usr/bin/" + a.Command + "\" \"$@\"\n"
	return os.WriteFile(filepath.Join(dir, "AppRun"), []byte(run), 0o755)
}

// AppImage builds an AppImage from the app with appimagetool. verbose
// shows the tool's output.
func AppImage(a App, work, out string, verbose bool) error {
	arch, ok := appImageArch[a.Arch]
	if !ok {
		return fmt.Errorf("no AppImage architecture for GOARCH %s", a.Arch)
	}
	if a.Icon == "" {
		return fmt.Errorf("an AppImage needs an icon: set app.icon in giode.toml")
	}
	dir := filepath.Join(work, a.Command+".AppDir")
	if err := AppDir(a, dir); err != nil {
		return err
	}
	tool, err := appImageTool()
	if err != nil {
		return err
	}
	cmd := exec.Command(tool, "--no-appstream", dir, out)
	cmd.Env = append(os.Environ(), "ARCH="+arch, "APPIMAGE_EXTRACT_AND_RUN=1")
	if verbose {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("appimagetool: %v\n%s", err, b)
	}
	return nil
}

// appImageTool returns the appimagetool to use: $GIODE_APPIMAGETOOL or
// the one on PATH. giode doesn't download it: it's a program the user
// should choose to trust.
func appImageTool() (string, error) {
	if p := os.Getenv("GIODE_APPIMAGETOOL"); p != "" {
		return p, nil
	}
	if p, err := exec.LookPath("appimagetool"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("appimagetool isn't installed: download it from https://github.com/AppImage/appimagetool/releases, make it executable and put it on PATH (or point $GIODE_APPIMAGETOOL at it)")
}

// defaultFlatpakPermissions let a Gio app show windows, use the GPU and
// the network, and play sound.
var defaultFlatpakPermissions = []string{"--share=ipc", "--socket=fallback-x11", "--socket=wayland", "--device=dri", "--share=network", "--socket=pulseaudio"}

// FlatpakPermissions are a's finish-args: FlatpakPermissions, or a
// desktop app's defaults.
func FlatpakPermissions(a App) []string {
	if len(a.FlatpakPermissions) > 0 {
		return a.FlatpakPermissions
	}
	return defaultFlatpakPermissions
}

// FlatpakRuntime is the freedesktop runtime version Flatpak builds use.
const FlatpakRuntime = "25.08"

// Flatpak writes a Flatpak manifest packaging the built binary into dir,
// with the files it installs, and builds a bundle (out) when
// flatpak-builder is installed. It reports whether it built one.
func Flatpak(a App, dir, out string, verbose bool) (bool, error) {
	files, err := Files(a, "/app")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	var sources, cmds []string
	for i, f := range files {
		name := fmt.Sprintf("f%d-%s", i, filepath.Base(f.Path))
		if err := os.WriteFile(filepath.Join(dir, name), f.Body, f.Mode); err != nil {
			return false, err
		}
		sources = append(sources, fmt.Sprintf(`{"type": "file", "path": %q}`, name))
		cmds = append(cmds, fmt.Sprintf(`"install -Dm%o %s %s"`, f.Mode, name, f.Path))
	}
	perms := a.FlatpakPermissions
	if len(perms) == 0 {
		perms = defaultFlatpakPermissions
	}
	q := make([]string, len(perms))
	for i, p := range perms {
		q[i] = fmt.Sprintf("%q", p)
	}
	manifest := fmt.Sprintf(`{
  "app-id": %q,
  "runtime": "org.freedesktop.Platform",
  "runtime-version": %q,
  "sdk": "org.freedesktop.Sdk",
  "command": %q,
  "finish-args": [%s],
  "modules": [
    {
      "name": %q,
      "buildsystem": "simple",
      "build-commands": [
        %s
      ],
      "sources": [
        %s
      ]
    }
  ]
}
`, a.ID, FlatpakRuntime, a.Command, strings.Join(q, ", "), a.Command, strings.Join(cmds, ",\n        "), strings.Join(sources, ",\n        "))
	mf := filepath.Join(dir, a.ID+".json")
	if err := os.WriteFile(mf, []byte(manifest), 0o644); err != nil {
		return false, err
	}
	builder, err := exec.LookPath("flatpak-builder")
	if err != nil {
		return false, nil
	}
	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if verbose {
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			return cmd.Run()
		}
		if b, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v\n%s", name, err, b)
		}
		return nil
	}
	if err := run(builder, "--user", "--install-deps-from=flathub", "--force-clean", "--repo=repo", "build", mf); err != nil {
		return false, err
	}
	if err := run("flatpak", "build-bundle", "repo", out, a.ID); err != nil {
		return false, err
	}
	return true, nil
}
