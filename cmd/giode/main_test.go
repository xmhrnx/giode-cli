package main

import (
	"archive/zip"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xmhrnx/giode-cli/internal/config"
)

func TestParse_FlagsAfterArgs(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	rel := fs.Bool("release", false, "")
	pos, err := parse(fs, []string{"android", "-release", "windows", "--", "-x"})
	if err != nil || !*rel || strings.Join(pos, " ") != "android windows -x" {
		t.Errorf("pos %v, release %v, err %v", pos, *rel, err)
	}
}

func TestGoVersion(t *testing.T) {
	for v, want := range map[string][2]int{"go1.26.6": {1, 26}, "go1.27rc1": {1, 27}, "devel": {0, 0}} {
		if maj, min := goVersion(v); maj != want[0] || min != want[1] {
			t.Errorf("goVersion(%q) = %d.%d", v, maj, min)
		}
	}
}

func TestBinaryName(t *testing.T) {
	c := &config.Config{App: config.App{Name: "My Notes: Pro"}}
	if got := binaryName(c); got != "MyNotesPro" {
		t.Errorf("binaryName = %q", got)
	}
}

func TestSetKeystore(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "giode.toml")
	os.WriteFile(p, []byte("[app]\nname = \"A\"\n\n[android]\nmin_sdk = 21\n"), 0o644)
	if err := setKeystore(p, "release.keystore"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "[android]\nmin_sdk = 21\nkeystore = \"release.keystore\"\n") {
		t.Errorf("giode.toml:\n%s", b)
	}
	os.WriteFile(p, []byte("[app]\nname = \"A\"\n"), 0o644)
	setKeystore(p, "k.keystore")
	b, _ = os.ReadFile(p)
	if !strings.HasSuffix(string(b), "\n\n[android]\nkeystore = \"k.keystore\"\n") {
		t.Errorf("without [android]:\n%s", b)
	}
}

func TestGogioOptions_Windows(t *testing.T) {
	dir := t.TempDir()
	c := &config.Config{Dir: dir, App: config.App{Name: "My App", ID: "com.example.app", Version: "1.2.3", Build: 7, Output: "dist"}}
	g, err := gogioOptions(c, "windows", buildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Target != "windows" || g.Version != "1.2.3.7" || g.Arch != "amd64" || g.Output != filepath.Join(dir, "dist", "windows", "MyApp.exe") {
		t.Errorf("options %+v", g)
	}
}

func TestSetTOMLValue_AfterComments(t *testing.T) {
	p := filepath.Join(t.TempDir(), "giode.toml")
	os.WriteFile(p, []byte("[windows.signing]\n# certificate = \"x.pfx\"\n\n[web]\nport = 1\n"), 0o644)
	if err := setTOMLValue(p, "windows.signing", "certificate", "codesign.pfx"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := "[windows.signing]\n# certificate = \"x.pfx\"\ncertificate = \"codesign.pfx\"\n\n[web]\nport = 1\n"
	if string(b) != want {
		t.Errorf("got\n%s\nwant\n%s", b, want)
	}
}

// TestSetTOMLValue_CRLF finds the table in a giode.toml with Windows
// line endings rather than adding a second one, which TOML rejects.
func TestSetTOMLValue_CRLF(t *testing.T) {
	p := filepath.Join(t.TempDir(), "giode.toml")
	os.WriteFile(p, []byte("[windows.signing]\r\n# certificate = \"x.pfx\"\r\n\r\n[web]\r\nport = 1\r\n"), 0o644)
	if err := setTOMLValue(p, "windows.signing", "certificate", "codesign.pfx"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := "[windows.signing]\r\n# certificate = \"x.pfx\"\r\ncertificate = \"codesign.pfx\"\r\n\r\n[web]\r\nport = 1\r\n"
	if string(b) != want {
		t.Errorf("got %q\nwant %q", b, want)
	}
	// A table that isn't there yet is added at the end.
	if err := setTOMLValue(p, "android", "keystore", "release.keystore"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	if want += "\r\n[android]\r\nkeystore = \"release.keystore\"\r\n"; string(b) != want {
		t.Errorf("got %q\nwant %q", b, want)
	}
}

func TestReplaceExecutable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "giode")
	os.WriteFile(p, []byte("old"), 0o755)
	if err := replaceExecutable(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Errorf("content %q", b)
	}
}

func TestZipTree(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "arm64-v8a"), 0o755)
	os.WriteFile(filepath.Join(root, "arm64-v8a", "libgio.so"), []byte("elf"), 0o644)
	os.MkdirAll(filepath.Join(root, "x86_64"), 0o755)
	os.WriteFile(filepath.Join(root, "x86_64", "libgio.so"), []byte("elf"), 0o644)
	dst := filepath.Join(t.TempDir(), "native-debug-symbols.zip")
	if err := zipTree(root, dst); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "arm64-v8a/libgio.so,x86_64/libgio.so" {
		t.Errorf("entries %v; Play wants ABI directories at the root", names)
	}
}
