package gogio

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeTestJar(t *testing.T, path string, entries ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, e := range entries {
		ew, _ := w.Create(e)
		ew.Write([]byte("bytes of " + e))
	}
	w.Close()
	f.Close()
}

func TestDexInput(t *testing.T) {
	dir := t.TempDir()
	loose := filepath.Join(dir, "classes", "org", "gioui", "Gio.class")
	os.MkdirAll(filepath.Dir(loose), 0o755)
	os.WriteFile(loose, []byte("gio"), 0o644)
	lib := filepath.Join(dir, "libs", "lib.jar")
	os.MkdirAll(filepath.Dir(lib), 0o755)
	writeTestJar(t, lib, "com/example/A.class", "META-INF/MANIFEST.MF", "META-INF/versions/9/com/example/A.class", "module-info.class", "res/strings.txt")

	out := filepath.Join(dir, "in.jar")
	if err := dexInput([]string{loose, lib}, dir, out); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	r.Close()
	if want := []string{"org/gioui/Gio.class", "com/example/A.class"}; !slices.Equal(names, want) {
		t.Errorf("entries %v, want %v", names, want)
	}

	dup := filepath.Join(dir, "libs", "dup.jar")
	writeTestJar(t, dup, "com/example/A.class")
	err = dexInput([]string{lib, dup}, dir, out)
	if err == nil || !strings.Contains(err.Error(), "com/example/A") {
		t.Errorf("a class in two jars: %v", err)
	}
}

// javac reads the sources from the argument file, paths with spaces
// and backslashes included.
func TestArgFileJavac(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("no javac")
	}
	dir := filepath.Join(t.TempDir(), "with space")
	os.MkdirAll(dir, 0o755)
	var srcs []string
	for _, n := range []string{"A", "B"} {
		p := filepath.Join(dir, n+".java")
		os.WriteFile(p, []byte("class "+n+" {}"), 0o644)
		srcs = append(srcs, p)
	}
	arg, err := argFile(filepath.Join(dir, "sources.txt"), srcs)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if b, err := exec.Command(javac, "-d", out, arg).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, b)
	}
	for _, n := range []string{"A", "B"} {
		if _, err := os.Stat(filepath.Join(out, n+".class")); err != nil {
			t.Error(err)
		}
	}
}
