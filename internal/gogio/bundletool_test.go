package gogio

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadChecksum(t *testing.T) {
	body := []byte("a jar")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	sum := sha256.Sum256(body)
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "tool.jar")
	if err := download(srv.URL, path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "a jar" {
		t.Errorf("downloaded %q", b)
	}
	bad := filepath.Join(dir, "bad.jar")
	if err := download(srv.URL, bad, "00"); err == nil {
		t.Error("a checksum mismatch was accepted")
	}
	if _, err := os.Stat(bad); err == nil {
		t.Error("a file that failed its checksum was left behind")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "bad.jar.*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestFindBundletoolPrefersBuildTools(t *testing.T) {
	bt := t.TempDir()
	jar := filepath.Join(bt, "bundletool-all-1.0.jar")
	os.WriteFile(jar, nil, 0o644)
	if got, err := findBundletool(bt); err != nil || got != jar {
		t.Errorf("findBundletool = %q, %v; want the build tools' jar", got, err)
	}
}
