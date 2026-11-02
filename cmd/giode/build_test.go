package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/xmhrnx/giode-cli/internal/config"
)

func TestAddTag(t *testing.T) {
	for _, c := range []struct{ tags, tag, want string }{
		{"", "giode_gio", "giode_gio"},
		{"foo bar", "giode_gio", "foo,bar,giode_gio"},
		{"foo,giode_gio", "giode_gio", "foo,giode_gio"},
	} {
		if got := addTag(c.tags, c.tag); got != c.want {
			t.Errorf("addTag(%q, %q) = %q", c.tags, c.tag, got)
		}
	}
}

var placeholder = regexp.MustCompile(`\{\{[A-Z_]+\}\}`)

func TestWorkflowSetup(t *testing.T) {
	plain := workflow(&config.Config{})
	if placeholder.MatchString(plain) || strings.Contains(plain, "msys2") || !strings.Contains(plain, "giode build $win -arch amd64 windows-installer msix") {
		t.Errorf("default workflow:\n%s", plain)
	}
	c := &config.Config{CI: config.CI{WindowsMSYS2: true, Setup: config.CISetup{
		MacOS: "brew install mpv", Linux: "sudo apt-get install -y libmpv-dev", Windows: "pacman -S --noconfirm mingw-w64-x86_64-mpv"}}}
	wf := workflow(c)
	for _, w := range []string{"brew install mpv", "libmpv-dev", "mingw-w64-x86_64-mpv", "runner: windows-latest", "msystem: MINGW64", "needs: [linux, macos, windows]", "shell: msys2 {0}"} {
		if !strings.Contains(wf, w) {
			t.Errorf("lacks %q", w)
		}
	}
	if placeholder.MatchString(wf) || strings.Count(wf, "windows-installer msix") != 1 {
		t.Errorf("windows built on two runners, or a placeholder is left:\n%s", wf)
	}
	arm := workflow(&config.Config{CI: config.CI{Arch: []string{"amd64", "arm64"}}})
	armMSYS2 := workflow(&config.Config{CI: config.CI{Arch: []string{"amd64", "arm64"}, WindowsMSYS2: true}})
	for _, w := range []string{"runner: ubuntu-24.04-arm", "giode build $win -arch amd64 windows-installer msix", "giode build $win -arch arm64 windows-installer msix", "name: linux-${{ matrix.arch }}"} {
		if !strings.Contains(arm, w) {
			t.Errorf("arm workflow lacks %q", w)
		}
	}
	for _, w := range []string{"runner: windows-11-arm", "msystem: CLANGARM64", "runner: ubuntu-24.04-arm"} {
		if !strings.Contains(armMSYS2, w) {
			t.Errorf("arm msys2 workflow lacks %q", w)
		}
	}
	if py, err := exec.LookPath("python3"); err == nil {
		for _, s := range []string{plain, wf, arm, armMSYS2} {
			cmd := exec.Command(py, "-c", "import sys, yaml; yaml.safe_load(sys.stdin)")
			cmd.Stdin = strings.NewReader(s)
			if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(out), "No module named") {
				t.Errorf("not YAML: %v\n%s", err, out)
			}
		}
	}
}

func TestLdflagsVersion(t *testing.T) {
	c := &config.Config{App: config.App{ID: "com.example.player", Name: "My Player", Version: "1.4.2", Build: 31, Ldflags: "-X main.version={version} -X main.build={build}"}}
	got := desktopLdflags(c, "android")
	for _, w := range []string{"-X main.version=1.4.2", "-X main.build=31", "-X github.com/xmhrnx/giode.AppVersion=1.4.2", "-X github.com/xmhrnx/giode.AppBuild=31", "-X github.com/xmhrnx/giode.AppID=com.example.player", "-X github.com/xmhrnx/giode.buildAppName=TXkgUGxheWVy"} {
		if !strings.Contains(got, w) {
			t.Errorf("ldflags %q lack %q", got, w)
		}
	}
}

func TestJavaSources(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"java/org/x/A.java", "java/org/x/notes.txt", "extra/B.java"} {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	c := &config.Config{Dir: dir, Android: config.Android{Java: []string{"java", "extra/B.java"}}}
	got, err := javaSources(c)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "java/org/x/A.java"), filepath.Join(dir, "extra/B.java")}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("sources %q, want %q", got, want)
	}
	c.Android.Java = []string{"missing"}
	if _, err := javaSources(c); err == nil {
		t.Error("a missing android.java directory gave no error")
	}
}

func TestWorkflowPlatformsTriggerTest(t *testing.T) {
	none := ""
	c := &config.Config{
		App: config.App{Platforms: []string{"windows", "android", "ios"}},
		CI:  config.CI{Trigger: "release", Test: &none},
	}
	wf := workflow(c)
	for _, w := range []string{"release:\n    types: [published]", "giode build $apk android aab", "windows-installer msix", "\n  ios:\n", "giode build ios-sim", "needs: [linux, ios]", "tag_name: ${{ github.ref_name }}"} {
		if !strings.Contains(wf, w) {
			t.Errorf("lacks %q", w)
		}
	}
	for _, w := range []string{"giode build web", "giode build deb rpm", "\n  macos:", "\n  test:", "push:\n    tags", "generate_release_notes"} {
		if strings.Contains(wf, w) {
			t.Errorf("has %q", w)
		}
	}

	def := workflow(&config.Config{App: config.App{Platforms: []string{"linux", "macos"}}})
	for _, w := range []string{"\n  test:", "go test ./...", "needs: test", "giode build deb rpm", "needs: [linux, macos]", "push:\n    tags: [\"v*\"]"} {
		if !strings.Contains(def, w) {
			t.Errorf("default lacks %q", w)
		}
	}
	for _, w := range []string{"giode build web", "android aab", "windows-installer", "\n  ios:"} {
		if strings.Contains(def, w) {
			t.Errorf("linux and macOS only, but has %q", w)
		}
	}
	if py, err := exec.LookPath("python3"); err == nil {
		for _, s := range []string{wf, def} {
			cmd := exec.Command(py, "-c", "import sys, yaml; yaml.safe_load(sys.stdin)")
			cmd.Stdin = strings.NewReader(s)
			if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(out), "No module named") {
				t.Errorf("not YAML: %v\n%s\n%s", err, out, s)
			}
		}
	}
}

func TestWorkflowBuildAndDispatch(t *testing.T) {
	tag := workflow(&config.Config{})
	if !strings.Contains(tag, "workflow_dispatch:") || !strings.Contains(tag, "GIODE_BUILD: ${{ github.run_number }}") {
		t.Error("the default workflow lacks workflow_dispatch or the run-number build")
	}
	off := false
	rel := workflow(&config.Config{CI: config.CI{Trigger: "release", Build: "tag"}})
	if strings.Contains(rel, "workflow_dispatch") || strings.Contains(rel, "GIODE_BUILD") {
		t.Errorf("release trigger with build = tag:\n%s", rel)
	}
	on := true
	if !strings.Contains(workflow(&config.Config{CI: config.CI{Trigger: "release", Dispatch: &on}}), "workflow_dispatch:") {
		t.Error("dispatch = true didn't add workflow_dispatch")
	}
	if strings.Contains(workflow(&config.Config{CI: config.CI{Dispatch: &off}}), "workflow_dispatch") {
		t.Error("dispatch = false kept workflow_dispatch")
	}
	if py, err := exec.LookPath("python3"); err == nil {
		cmd := exec.Command(py, "-c", "import sys, yaml; yaml.safe_load(sys.stdin)")
		cmd.Stdin = strings.NewReader(rel)
		if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(out), "No module named") {
			t.Errorf("not YAML: %v\n%s", err, out)
		}
	}
}

func TestLdflagsLegacyGiodePath(t *testing.T) {
	c := &config.Config{App: config.App{ID: "com.example.player", Version: "1.0.0", Build: 3}}
	got := desktopLdflags(c, "linux")
	for _, w := range []string{"-X github.com/xmhrnx/giode.AppVersion=1.0.0", "-X github.com/mehran9675/giode.AppVersion=1.0.0", "-X github.com/mehran9675/giode.AppID=com.example.player"} {
		if !strings.Contains(got, w) {
			t.Errorf("ldflags %q lack %q", got, w)
		}
	}
	// Other packages' settings aren't repeated.
	if got := strings.Join(withLegacyGiodePath([]string{"-X", "main.version=1", "-X", "github.com/xmhrnx/giode-cli.x=1"}), " "); strings.Contains(got, "mehran9675") {
		t.Errorf("repeated a non-giode setting: %s", got)
	}
}

func TestMoveGiodeImports(t *testing.T) {
	dir := t.TempDir()
	src := "package main\n\nimport (\n\t\"github.com/mehran9675/giode\"\n\t\"github.com/mehran9675/giode/styles\"\n\t\"github.com/mehran9675/giodex\"\n)\n"
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644)
	os.MkdirAll(filepath.Join(dir, "vendor"), 0o755)
	os.WriteFile(filepath.Join(dir, "vendor", "v.go"), []byte(src), 0o644)
	n, err := moveGiodeImports(dir)
	if err != nil || n != 1 {
		t.Fatalf("moved %d files, err %v; want 1", n, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	want := "package main\n\nimport (\n\t\"github.com/xmhrnx/giode\"\n\t\"github.com/xmhrnx/giode/styles\"\n\t\"github.com/mehran9675/giodex\"\n)\n"
	if string(b) != want {
		t.Errorf("got:\n%s", b)
	}
}
