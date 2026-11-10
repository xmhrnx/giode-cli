package giopatch

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPrepare patches the Gio version giode builds with and checks the
// build resolves gioui.org to the patched copy. It needs the module in
// the module cache or a reachable proxy.
func TestPrepare(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	app := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("GOFLAGS", "-mod=mod")
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(app, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/app\n\ngo 1.24\n\nrequire gioui.org v0.10.2\n")
	write("main.go", "package main\n\nimport _ \"gioui.org/app\"\n\nfunc main() {}\n")
	if out, err := exec.Command("sh", "-c", "cd "+app+" && go mod tidy").CombinedOutput(); err != nil {
		t.Skipf("can't fetch Gio: %v\n%s", err, out)
	}
	t.Setenv("GOFLAGS", "")

	modfile, cleanup, err := Prepare(app, All)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	cmd := exec.Command("go", "list", "-modfile="+modfile, "-f", "{{.Dir}}", "gioui.org/app")
	cmd.Dir = app
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	dir := strings.TrimSpace(string(out))
	if !strings.Contains(dir, filepath.Join("giode", "gio")) {
		t.Fatalf("gioui.org/app resolved to %s, not the patched copy", dir)
	}
	src, err := os.ReadFile(filepath.Join(dir, "os_android.go"))
	if err != nil {
		t.Fatal(err)
	}
	win, err := os.ReadFile(filepath.Join(dir, "os_windows.go"))
	if err != nil || !strings.Contains(string(win), "giodeEraseBackground(uintptr(hwnd), wParam)") {
		t.Errorf("os_windows.go doesn't fill the background before the first frame (%v)", err)
	}
	for _, want := range []string{"windows.Margins{1, 1, 1, 1}", "giodeDarkFrame(uintptr(w.hwnd))"} {
		if !strings.Contains(string(win), want) {
			t.Errorf("os_windows.go lacks %q", want)
		}
	}
	for _, want := range []string{"giode: ask for the next frame", "func snippetsAgree"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("os_android.go lacks %q", want)
		}
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "os_android.go", src, 0); err != nil {
		t.Errorf("patched os_android.go doesn't parse: %v", err)
	}
	// gioui.org/shader picks the simulator's Metal libraries in
	// simulator builds on any architecture.
	cmd = exec.Command("go", "list", "-modfile="+modfile, "-f", "{{.Dir}}", "gioui.org/shader/gio")
	cmd.Dir = app
	out, err = cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	shaderDir := strings.TrimSpace(string(out))
	if !strings.Contains(shaderDir, filepath.Join("giode", "gio-shader")) {
		t.Fatalf("gioui.org/shader/gio resolved to %s, not the patched copy", shaderDir)
	}
	shaders, err := os.ReadFile(filepath.Join(shaderDir, "shaders.go"))
	if err != nil || !strings.Contains(string(shaders), `runtime.GOARCH == "amd64" || giodeIOSSimulator`) || strings.Contains(string(shaders), "if runtime.GOARCH == \"amd64\" {") {
		t.Errorf("gioui.org/shader/gio doesn't pick the simulator's libraries by tag (%v)", err)
	}
	for _, tags := range []string{"", TagIOSSimulator} {
		cmd = exec.Command("go", "build", "-modfile="+modfile, "-tags="+tags, "gioui.org/shader/gio")
		cmd.Dir = app
		cmd.Env = append(os.Environ(), "GOOS=ios", "GOARCH=arm64", "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("the patched shaders don't build with tags %q: %v\n%s", tags, err, out)
		}
	}

	if _, err := os.Stat(filepath.Join(app, "go.mod")); err != nil {
		t.Error(err)
	}
	if b, _ := os.ReadFile(filepath.Join(app, "go.mod")); strings.Contains(string(b), "replace") {
		t.Error("the app's go.mod was changed")
	}

	// The underlay and file drag hooks build on the platforms buildable here.
	write("main.go", `package main

import "gioui.org/app"

func main() {
	app.GiodeSetUnderlay(func(t app.GiodeTarget) { _ = t.Framebuffer }, true)
	app.GiodeSetUnderlayHooks(func() {}, func(app.GiodeTarget) {})
	app.GiodeSetFileDrag(func(kind int, x, y float32, uris string) {})
	app.GiodeSetBackground(0x11, 0x22, 0x33, true)
	new(app.Window).GiodeInject(nil)
}
`)
	envs := [][]string{{"GOOS=windows"}, {"GOOS=js", "GOARCH=wasm"}}
	// Gio's Linux backends are cgo: they build on Linux, with the
	// X11, Wayland and xkbcommon headers.
	if runtime.GOOS == "linux" && exec.Command("pkg-config", "--exists", "x11", "wayland-client", "xkbcommon", "egl").Run() == nil {
		envs = append(envs, []string{"GOOS=linux"})
	} else {
		t.Log("not building for Linux: needs Linux with Gio's development packages")
	}
	for _, env := range envs {
		cmd := exec.Command("go", "build", "-modfile="+modfile, "-o", os.DevNull, ".")
		cmd.Dir = app
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%v: %v\n%s", env, err, out)
		}
	}

	// A second build reuses the copy.
	m2, c2, err := Prepare(app, All)
	if err != nil {
		t.Fatal(err)
	}
	c2()
	_ = m2
}

// TestPrepare_NotModuleMode skips the patch when go isn't in module
// mode, where -modfile is refused.
func TestPrepare_NotModuleMode(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	t.Setenv("GO111MODULE", "off")
	_, _, err := Prepare(t.TempDir(), Android)
	if !errors.Is(err, ErrSkipped) {
		t.Errorf("GO111MODULE=off: %v, want ErrSkipped", err)
	}
}

func TestApply_MismatchSkips(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "os_android.go"), []byte("package app\n"), 0o644)
	err := apply(dir, Android)
	if err == nil || !strings.Contains(err.Error(), ErrSkipped.Error()) {
		t.Errorf("apply to other code: %v, want ErrSkipped", err)
	}
}
