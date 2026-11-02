package config

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const ctxToml = `[app]
name = "Agent"
id = "com.example.agent"

[[context_actions]]
id = "ask"
label = "Ask Agent"
extensions = [".md", "txt"]

[[context_actions]]
id = "here"
label = "Open Agent here"
folders = true
background = true
`

func TestContextActions(t *testing.T) {
	c, err := load(t, ctxToml)
	if err != nil {
		t.Fatal(err)
	}
	verbs := c.WindowsVerbs()
	var keys []string
	for _, v := range verbs {
		keys = append(keys, v.Key+" "+v.Arg)
	}
	want := []string{
		`SystemFileAssociations\.md\shell\com-example-agent.ask %1`,
		`SystemFileAssociations\.txt\shell\com-example-agent.ask %1`,
		`Directory\shell\com-example-agent.here %1`,
		`Directory\Background\shell\com-example-agent.here %V`,
	}
	if strings.Join(keys, "|") != strings.Join(want, "|") {
		t.Errorf("verbs:\n%s\nwant\n%s", strings.Join(keys, "\n"), strings.Join(want, "\n"))
	}

	flag := c.ContextActionsLdflag()
	const prefix = "-X github.com/xmhrnx/giode.buildContextActions="
	if !strings.HasPrefix(flag, prefix) || strings.ContainsAny(flag[len(prefix):], " '\"") {
		t.Fatalf("ldflag %q", flag)
	}
	b, err := base64.URLEncoding.DecodeString(flag[len(prefix):])
	if err != nil {
		t.Fatal(err)
	}
	var back []ContextAction
	json.Unmarshal(b, &back)
	if len(back) != 2 || back[1].ID != "here" || !back[1].Background {
		t.Errorf("round trip: %+v", back)
	}

	plist, err := c.MacOSPlist()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"<key>NSServices</key>",
		"<key>NSMessage</key><string>giodeContextAction</string>",
		"<key>NSUserData</key><string>ask</string>",
		"<string>net.daringfireball.markdown</string><string>public.plain-text</string>",
		"<key>default</key><string>Open Agent here</string>",
		"<string>public.folder</string>",
	} {
		if !strings.Contains(plist, w) {
			t.Errorf("plist lacks %s:\n%s", w, plist)
		}
	}
}

func TestContextActions_Validate(t *testing.T) {
	_, err := load(t, "[app]\nname = \"A\"\nid = \"com.example.a\"\n[[context_actions]]\nid = \"bad id\"\n")
	if err == nil || !strings.Contains(err.Error(), "context_actions[0].id") || !strings.Contains(err.Error(), "label is required") {
		t.Errorf("err = %v", err)
	}
}

func TestPrivacyManifest(t *testing.T) {
	c, err := load(t, `[app]
name = "A"
id = "com.example.a"

[privacy]
tracking_domains = ["t.example.com"]
[[privacy.collected]]
type = "email_address"
linked = true
purposes = ["app_functionality", "NSPrivacyCollectedDataTypePurposeAnalytics"]
[privacy.api]
user_defaults = ["CA92.1"]
`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.PrivacyManifest()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, w := range []string{
		"<key>NSPrivacyTracking</key>\n\t<false/>",
		"<string>t.example.com</string>",
		"<key>NSPrivacyCollectedDataType</key><string>NSPrivacyCollectedDataTypeEmailAddress</string>",
		"<key>NSPrivacyCollectedDataTypeLinked</key><true/>",
		"<string>NSPrivacyCollectedDataTypePurposeAppFunctionality</string><string>NSPrivacyCollectedDataTypePurposeAnalytics</string>",
		"<string>NSPrivacyAccessedAPICategoryFileTimestamp</string><key>NSPrivacyAccessedAPITypeReasons</key><array><string>C617.1</string></array>",
		"<string>NSPrivacyAccessedAPICategorySystemBootTime</string>",
		"<string>NSPrivacyAccessedAPICategoryUserDefaults</string><key>NSPrivacyAccessedAPITypeReasons</key><array><string>CA92.1</string></array>",
	} {
		if !strings.Contains(s, w) {
			t.Errorf("manifest lacks %q:\n%s", w, s)
		}
	}
	if _, err := load(t, "[app]\nname = \"A\"\nid = \"com.example.a\"\n[[privacy.collected]]\ntype = \"shoe_size\"\n"); err == nil {
		t.Error("unknown data type accepted")
	}
}
