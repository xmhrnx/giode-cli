package config

import (
	"encoding/json"
	"strings"
	"testing"
)

const linksToml = `[app]
name = "Agent"
id = "com.example.agent"
links = ["https://example.com/app/*", "https://example.com/share/*/view", "https://go.example.com"]
`

func TestLinks(t *testing.T) {
	c, err := load(t, linksToml)
	if err != nil {
		t.Fatal(err)
	}
	x := c.AndroidActivityXML()
	for _, w := range []string{
		`<intent-filter android:autoVerify="true">`,
		`<data android:scheme="https" android:host="example.com" android:pathPrefix="/app/"/>`,
		`<data android:scheme="https" android:host="example.com" android:pathPattern="/share/.*/view"/>`,
		`<data android:scheme="https" android:host="go.example.com"/>`,
	} {
		if !strings.Contains(x, w) {
			t.Errorf("intent filters lack %s:\n%s", w, x)
		}
	}
	ents := c.AppleEntitlements()
	d, _ := ents["com.apple.developer.associated-domains"].([]string)
	if strings.Join(d, ",") != "applinks:example.com,applinks:go.example.com" {
		t.Errorf("associated domains %v", d)
	}
	aasa, err := c.AppleAppSiteAssociation("example.com", "ABCDE12345")
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Applinks struct {
			Details []struct {
				AppIDs     []string            `json:"appIDs"`
				Components []map[string]string `json:"components"`
			} `json:"details"`
		} `json:"applinks"`
	}
	json.Unmarshal(aasa, &a)
	det := a.Applinks.Details[0]
	if det.AppIDs[0] != "ABCDE12345.com.example.agent" || len(det.Components) != 2 || det.Components[0]["/"] != "/app/*" {
		t.Errorf("aasa %s", aasa)
	}
	al, _ := c.AssetLinks(c.LinkHosts()[0], []string{"AA:BB"})
	if !strings.Contains(string(al), `"package_name": "com.example.agent"`) || !strings.Contains(string(al), `"AA:BB"`) ||
		!strings.Contains(string(al), "handle_all_urls") || strings.Contains(string(al), "get_login_creds") {
		t.Errorf("assetlinks %s", al)
	}
	if _, err := load(t, "[app]\nname = \"A\"\nid = \"com.example.a\"\nlinks = [\"http://example.com\"]\n"); err == nil {
		t.Error("an http link was accepted")
	}
}

func TestLoad_CIOverrides(t *testing.T) {
	t.Setenv("GIODE_VERSION", "v3.1.4")
	t.Setenv("GIODE_BUILD", "77")
	c, err := load(t, "[app]\nname = \"A\"\nid = \"com.example.a\"\nversion = \"1.0.0\"\nbuild = 2\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.App.Version != "3.1.4" || c.App.Build != 77 {
		t.Errorf("version %s build %d, want 3.1.4 and 77", c.App.Version, c.App.Build)
	}
	t.Setenv("GIODE_BUILD", "x")
	if _, err := load(t, "[app]\nname = \"A\"\nid = \"com.example.a\"\n"); err == nil {
		t.Error("a non-numeric GIODE_BUILD was accepted")
	}
}

func TestPushEntitlements(t *testing.T) {
	c := &Config{IOS: IOS{Push: "production"}, MacOS: MacOS{Push: "development"}}
	if e := c.IOSEntitlements(); e["aps-environment"] != "production" {
		t.Errorf("iOS %v", e)
	}
	if e := c.MacOSEntitlements(); e["com.apple.developer.aps-environment"] != "development" || e["aps-environment"] != nil {
		t.Errorf("macOS %v", e)
	}
	plist, err := c.IOSPlist()
	if err != nil || !strings.Contains(plist, "<key>UIBackgroundModes</key>") || !strings.Contains(plist, "<string>remote-notification</string>") {
		t.Errorf("plist %v\n%s", err, plist)
	}
	c.IOS.Push = "prod"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "ios.push") {
		t.Errorf("validate: %v", err)
	}
}

func TestPasskeyDomains(t *testing.T) {
	c, err := load(t, "[app]\nname = \"A\"\nid = \"com.example.a\"\nlinks = [\"https://example.com/app/*\"]\npasskeys = [\"example.com\", \"login.example.org\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	ents := c.AppleEntitlements()["com.apple.developer.associated-domains"].([]string)
	if strings.Join(ents, " ") != "applinks:example.com webcredentials:example.com webcredentials:login.example.org" {
		t.Errorf("entitlements %v", ents)
	}
	if h := c.WellKnownHosts(); strings.Join(h, " ") != "example.com login.example.org" {
		t.Errorf("hosts %v", h)
	}
	al, _ := c.AssetLinks("example.com", nil)
	if !strings.Contains(string(al), "handle_all_urls") || !strings.Contains(string(al), "get_login_creds") {
		t.Errorf("assetlinks %s", al)
	}
	al, _ = c.AssetLinks("login.example.org", nil)
	if strings.Contains(string(al), "handle_all_urls") || !strings.Contains(string(al), "get_login_creds") {
		t.Errorf("passkey-only assetlinks %s", al)
	}
	aasa, _ := c.AppleAppSiteAssociation("login.example.org", "TEAM")
	if strings.Contains(string(aasa), "applinks") || !strings.Contains(string(aasa), `"TEAM.com.example.a"`) || !strings.Contains(string(aasa), "webcredentials") {
		t.Errorf("aasa %s", aasa)
	}
}
