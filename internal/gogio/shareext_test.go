package gogio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShareExtPlist(t *testing.T) {
	bi := &buildInfo{name: "Easy & Send", appID: "com.example.send", target: "ios", minsdk: 15}
	bi.version.VersionCode = 7
	p := shareExtPlist(bi, &ShareExtension{AppGroup: "group.com.example.send", Scheme: "giode-share-com.example.send"})
	for _, want := range []string{
		"<string>com.example.send.share</string>",
		"<string>Easy &amp; Send</string>",
		"<string>XPC!</string>",
		"<string>com.apple.share-services</string>",
		"<string>GiodeShareViewController</string>",
		"<string>group.com.example.send</string>",
		"<string>7</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %s", want)
		}
	}
	checkPlist(t, p)
	e := entitlementsXML(map[string]any{"com.apple.security.application-groups": []string{"group.a"}, "b": true, "c": "x"})
	if !strings.Contains(e, "<array>\n\t\t<string>group.a</string>") || !strings.Contains(e, "<true/>") {
		t.Errorf("entitlements:\n%s", e)
	}
	checkPlist(t, e)
}

// checkPlist parses a plist with Python's plistlib, when there is one.
func checkPlist(t *testing.T, p string) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		return
	}
	f := filepath.Join(t.TempDir(), "x.plist")
	os.WriteFile(f, []byte(p), 0o644)
	if out, err := exec.Command(py, "-c", "import plistlib,sys; plistlib.load(open(sys.argv[1],'rb'))", f).CombinedOutput(); err != nil {
		t.Errorf("invalid plist: %v\n%s", err, out)
	}
}
