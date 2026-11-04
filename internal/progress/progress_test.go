package progress

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCommandLabel(t *testing.T) {
	goBuild := exec.Command("go", "build", "-o", "x", ".")
	goBuild.Env = []string{"GOOS=android", "GOARCH=arm64"}
	win := exec.Command("go", "build", ".")
	win.Env = []string{"GOOS=windows", "GOARCH=amd64"}
	for _, c := range []struct {
		cmd  *exec.Cmd
		want string
	}{
		{goBuild, "Compiling Go · arm64"},
		{win, "Compiling Go · windows/amd64"},
		{exec.Command("go", "list", "-json"), ""},
		{exec.Command("/sdk/build-tools/34.0.0/aapt2", "link", "-o", "x"), "Linking resources"},
		{exec.Command("xcrun", "--sdk", "iphoneos", "--show-sdk-path"), ""},
		{exec.Command("xcrun", "lipo", "-o", "app", "-create", "a", "b"), "Merging architectures"},
		{exec.Command("xcrun", "--sdk", "iphoneos", "actool", "x"), "Compiling the asset catalog"},
		{exec.Command("codesign", "--sign", "-", "--force", "/tmp/Demo.app"), "Code signing Demo.app"},
		{exec.Command("/usr/libexec/PlistBuddy", "-c", "x"), ""},
		{exec.Command("makensis", "-V2", "x.nsi"), "Building the setup program (NSIS)"},
		{exec.Command("mytool", "x"), "Running mytool"},
	} {
		if got := CommandLabel(c.cmd); got != c.want {
			t.Errorf("%v: got %q, want %q", c.cmd.Args, got, c.want)
		}
	}
}

func TestPlainReport(t *testing.T) {
	var out bytes.Buffer
	r := &Reporter{w: &out, start: time.Now(), width: func() int { return 80 }}
	current.Store(r)
	defer current.Store(nil)
	r.Header("Demo", "1.0.0 (3)", []string{"web", "linux"})
	r.Target("web", 1, 2)
	done := Begin("Compiling Go · wasm")
	time.Sleep(110 * time.Millisecond)
	done(nil)
	Begin("Quick")(nil) // under 100ms: no line
	Logf("a note")
	r.TargetDone("dist/web", nil)
	r.Target("linux", 2, 2)
	Begin("Compiling Go")(errors.New("boom"))
	r.TargetDone("", errors.New("go build: exit status 1\nmain.go:3: undefined: x"))
	r.Summary()
	s := out.String()
	for _, want := range []string{
		"giode build  Demo 1.0.0 (3)  ·  web, linux",
		"▸ web  1/2",
		"✓ Compiling Go · wasm",
		"· a note",
		"✓ web  dist/web",
		"✗ Compiling Go",
		"✗ linux failed after",
		"    main.go:3: undefined: x",
		"1 built, 1 failed",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Quick") || strings.Contains(s, "\x1b[") {
		t.Errorf("quick step or colour codes in plain output:\n%s", s)
	}
}

func TestNoReporter(t *testing.T) {
	if Running() || Live() {
		t.Fatal("a reporter is running")
	}
	Begin("nothing")(nil)
	Command(exec.Command("go", "build"))(nil)
}
