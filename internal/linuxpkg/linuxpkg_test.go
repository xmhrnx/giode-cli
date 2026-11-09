package linuxpkg

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testApp builds a tiny program and returns an App packaging it.
func testApp(t *testing.T) App {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello from giode\") }\n"), 0o644)
	bin := filepath.Join(dir, "hello")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	icon := filepath.Join(dir, "icon.png")
	f, _ := os.Create(icon)
	png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 512, 512)))
	f.Close()
	return App{Name: "Hello App", ID: "com.example.hello", Version: "1.2.3", Build: 4,
		Summary: "Says hello", Description: "A test app.\n\nIt says hello.", Publisher: "Example Ltd",
		Website: "https://example.com", License: "MIT", Categories: []string{"Utility"},
		Binary: bin, Command: "hello-app", Icon: icon, Arch: runtime.GOARCH}
}

func TestDeb(t *testing.T) {
	a := testApp(t)
	out := filepath.Join(t.TempDir(), "hello.deb")
	if err := Deb(a, out); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("no dpkg-deb to check the package")
	}
	info, err := exec.Command("dpkg-deb", "--info", out).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, info)
	}
	for _, w := range []string{"Package: hello-app", "Version: 1.2.3-4", "Maintainer: Example Ltd <noreply@example.com>", "Depends: libc6", " It says hello."} {
		if !strings.Contains(string(info), w) {
			t.Errorf("info lacks %q:\n%s", w, info)
		}
	}
	contents, _ := exec.Command("dpkg-deb", "--contents", out).CombinedOutput()
	for _, w := range []string{"./usr/bin/hello-app", "./usr/share/applications/com.example.hello.desktop", "./usr/share/icons/hicolor/512x512/apps/com.example.hello.png", "./usr/share/metainfo/com.example.hello.metainfo.xml"} {
		if !strings.Contains(string(contents), w) {
			t.Errorf("contents lack %s:\n%s", w, contents)
		}
	}
	// Extract it and run the program.
	root := t.TempDir()
	if b, err := exec.Command("dpkg-deb", "-x", out, root).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if b, _ := exec.Command(filepath.Join(root, "usr/bin/hello-app")).Output(); strings.TrimSpace(string(b)) != "hello from giode" {
		t.Errorf("extracted binary printed %q", b)
	}
}

func TestRPM(t *testing.T) {
	a := testApp(t)
	out := filepath.Join(t.TempDir(), "hello.rpm")
	if err := RPM(a, out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if len(b) < 104 || binary.BigEndian.Uint32(b) != 0xedabeedb || binary.BigEndian.Uint32(b[96:]) != 0x8eade801 {
		t.Fatalf("not an RPM: % x", b[:min(len(b), 16)])
	}
	if !bytes.Contains(b, []byte("hello-app")) || !bytes.Contains(b, []byte("libEGL.so.1()")) {
		t.Error("header lacks the name or requirements")
	}
	if rpm, err := exec.LookPath("rpm"); err == nil {
		info, err := exec.Command(rpm, "-qip", out).CombinedOutput()
		if err != nil || !strings.Contains(string(info), "1.2.3") {
			t.Errorf("rpm -qip: %v\n%s", err, info)
		}
	}
}

func TestMetainfoAndDesktop(t *testing.T) {
	a := testApp(t)
	d := DesktopEntry(a)
	for _, w := range []string{"Name=Hello App", "Exec=hello-app %U", "Icon=com.example.hello", "Categories=Utility;"} {
		if !strings.Contains(d, w) {
			t.Errorf("desktop entry lacks %q:\n%s", w, d)
		}
	}
	path := filepath.Join(t.TempDir(), "com.example.hello.metainfo.xml")
	os.WriteFile(path, []byte(Metainfo(a)), 0o644)
	if tool, err := exec.LookPath("appstreamcli"); err == nil {
		if b, err := exec.Command(tool, "validate", "--no-net", path).CombinedOutput(); err != nil && strings.Contains(string(b), "E:") {
			t.Errorf("appstreamcli validate: %v\n%s", err, b)
		}
	}
	if tool, err := exec.LookPath("desktop-file-validate"); err == nil {
		p := filepath.Join(t.TempDir(), "com.example.hello.desktop")
		os.WriteFile(p, []byte(d), 0o644)
		if b, err := exec.Command(tool, p).CombinedOutput(); err != nil {
			t.Errorf("desktop-file-validate: %v\n%s", err, b)
		}
	}
}

// TestAppImage builds and runs an AppImage when appimagetool is
// installed.
func TestAppImage(t *testing.T) {
	if _, err := appImageTool(); err != nil {
		t.Skip(err)
	}
	a := testApp(t)
	work := t.TempDir()
	out := filepath.Join(work, "Hello.AppImage")
	if err := AppImage(a, work, out, false); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(out)
	cmd.Env = append(os.Environ(), "APPIMAGE_EXTRACT_AND_RUN=1")
	b, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(b)) != "hello from giode" {
		t.Errorf("AppImage printed %q (%v)", b, err)
	}
}

func TestAppDir(t *testing.T) {
	a := testApp(t)
	dir := filepath.Join(t.TempDir(), "Hello.AppDir")
	if err := AppDir(a, dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"AppRun", "com.example.hello.desktop", "com.example.hello.png", "usr/bin/hello-app"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
	// AppRun runs the program, where Linux programs run.
	if runtime.GOOS != "linux" {
		return
	}
	if b, err := exec.Command(filepath.Join(dir, "AppRun")).Output(); err != nil || strings.TrimSpace(string(b)) != "hello from giode" {
		t.Errorf("AppRun printed %q (%v)", b, err)
	}
}

func TestFlatpakManifest(t *testing.T) {
	if _, err := exec.LookPath("flatpak-builder"); err == nil {
		t.Skip("flatpak-builder is installed; this test only checks the manifest")
	}
	a := testApp(t)
	dir := t.TempDir()
	if _, err := Flatpak(a, dir, filepath.Join(dir, "hello.flatpak"), false); err != nil {
		t.Fatal(err)
	}
	m, err := os.ReadFile(filepath.Join(dir, "com.example.hello.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{`"app-id": "com.example.hello"`, `"command": "hello-app"`, `"--socket=wayland"`, `install -Dm755 f0-hello-app /app/bin/hello-app`} {
		if !strings.Contains(string(m), w) {
			t.Errorf("manifest lacks %s:\n%s", w, m)
		}
	}
}

func TestMetainfoScreenshots(t *testing.T) {
	a := testApp(t)
	a.Screenshots = []Screenshot{{URL: "https://example.com/main.png", Caption: "Sending files"}, {URL: "https://example.com/receive.png"}}
	m := Metainfo(a)
	for _, w := range []string{
		"<screenshots>\n    <screenshot type=\"default\">\n      <image>https://example.com/main.png</image>\n      <caption>Sending files</caption>\n    </screenshot>",
		"<screenshot>\n      <image>https://example.com/receive.png</image>\n    </screenshot>\n  </screenshots>",
	} {
		if !strings.Contains(m, w) {
			t.Errorf("metainfo lacks %q:\n%s", w, m)
		}
	}
	if tool, err := exec.LookPath("appstreamcli"); err == nil {
		path := filepath.Join(t.TempDir(), a.ID+".metainfo.xml")
		os.WriteFile(path, []byte(m), 0o644)
		if b, err := exec.Command(tool, "validate", "--no-net", path).CombinedOutput(); err != nil && strings.Contains(string(b), "E:") {
			t.Errorf("appstreamcli validate: %v\n%s", err, b)
		}
	}
}
