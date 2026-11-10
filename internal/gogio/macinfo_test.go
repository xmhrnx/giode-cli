package gogio

import (
	"strings"
	"testing"
)

// giode.toml's Info.plist keys replace the template's.
func TestMacInfoKeysReplaced(t *testing.T) {
	old := extras
	defer func() { extras = old }()
	extras.InfoPlist = "\t<key>LSApplicationCategoryType</key>\n\t<string>public.app-category.utilities</string>\n\t<key>CFBundleName</key>\n\t<string>Easy Send</string>"
	var b macBuilder
	b.setInfo(&buildInfo{appID: "app.easysend", version: Semver{Major: 1, Minor: 2, Patch: 3, VersionCode: 4}}, "EasySend")
	m := string(b.Manifest)
	if strings.Count(m, "<key>CFBundleName</key>") != 1 || !strings.Contains(m, "<string>Easy Send</string>") {
		t.Errorf("CFBundleName twice, or not giode.toml's:\n%s", m)
	}
	for _, w := range []string{"<key>CFBundlePackageType</key>\n\t<string>APPL</string>", "<key>LSApplicationCategoryType</key>", "<key>CFBundleIdentifier</key>\n\t<string>app.easysend</string>"} {
		if !strings.Contains(m, w) {
			t.Errorf("lacks %q:\n%s", w, m)
		}
	}
}

// LSMinimumSystemVersion is [macos] min_os, 11 by default.
func TestMacInfoMinimumSystemVersion(t *testing.T) {
	old := extras
	defer func() { extras = old }()
	extras.InfoPlist = ""
	for minsdk, want := range map[int]string{0: "11.0", 12: "12.0"} {
		var b macBuilder
		b.setInfo(&buildInfo{appID: "app.easysend", minsdk: minsdk}, "EasySend")
		if w := "<key>LSMinimumSystemVersion</key>\n\t<string>" + want + "</string>"; !strings.Contains(string(b.Manifest), w) {
			t.Errorf("min_os %d: lacks %q:\n%s", minsdk, w, b.Manifest)
		}
	}
}
