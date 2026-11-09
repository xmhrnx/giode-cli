package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlathubHelpers(t *testing.T) {
	if got := escapeModule("github.com/BurntSushi/toml"); got != "github.com/!burnt!sushi/toml" {
		t.Errorf("escapeModule: %s", got)
	}
	for in, want := range map[string]string{
		"git@github.com:xmhrnx/easysend.git":       "https://github.com/xmhrnx/easysend.git",
		"ssh://git@github.com/xmhrnx/easysend.git": "https://github.com/xmhrnx/easysend.git",
		"https://github.com/xmhrnx/easysend":       "https://github.com/xmhrnx/easysend",
	} {
		if got := httpsRepo(in); got != want {
			t.Errorf("httpsRepo(%s) = %s", in, got)
		}
	}
	if got := localName("/app/share/icons/hicolor/128x128/apps/app.easysend.png"); got != "app.easysend-128x128.png" {
		t.Errorf("localName: %s", got)
	}
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("shellQuote: %s", got)
	}
}

// Every go.sum file is a source in the proxy layout: .mod files, and
// .zip files with their .info; Gio's zip is kept for its hash only when
// it's patched.
func TestGoModuleSources(t *testing.T) {
	mod := t.TempDir()
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/app\n\ngo 1.22\n"), 0o644)
	os.WriteFile(filepath.Join(mod, "go.sum"), []byte(strings.Join([]string{
		"github.com/BurntSushi/toml v1.4.0 h1:x=",
		"github.com/BurntSushi/toml v1.4.0/go.mod h1:y=",
		"gioui.org v0.10.2 h1:z=",
		"gioui.org v0.10.2/go.mod h1:w=",
	}, "\n")+"\n"), 0o644)
	files := map[string]string{
		"github.com/!burnt!sushi/toml/@v/v1.4.0.zip": "zip",
		"github.com/!burnt!sushi/toml/@v/v1.4.0.mod": "mod",
		"gioui.org/@v/v0.10.2.zip":                   "gio zip",
		"gioui.org/@v/v0.10.2.mod":                   "gio mod",
	}
	for rel, body := range files {
		p := filepath.Join(cache, "cache", "download", filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

	srcs, err := goModuleSources(mod, true)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]flatpakSource{}
	for _, s := range srcs {
		byName[s.Dest+"/"+s.DestFilename] = s
	}
	toml := byName["goproxy/github.com/!burnt!sushi/toml/@v/v1.4.0.zip"]
	if toml.Type != "file" || toml.SHA256 != sum("zip") || toml.URL != "https://proxy.golang.org/github.com/!burnt!sushi/toml/@v/v1.4.0.zip" {
		t.Errorf("toml zip: %+v", toml)
	}
	if info := byName["goproxy/github.com/!burnt!sushi/toml/@v/v1.4.0.info"]; info.Type != "inline" || info.Contents != `{"Version":"v1.4.0"}` {
		t.Errorf("toml info: %+v", info)
	}
	if m := byName["goproxy/gioui.org/@v/v0.10.2.mod"]; m.Type != "file" || m.SHA256 != sum("gio mod") {
		t.Errorf("gio mod: %+v", m)
	}
	if z := byName["goproxy/gioui.org/@v/v0.10.2.zip"]; z.Type != "gio-zip" || modSHA(srcs, "gioui.org", "v0.10.2") != sum("gio zip") {
		t.Errorf("gio zip, patched: %+v", z)
	}
}
