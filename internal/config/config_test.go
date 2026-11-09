package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, src string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestLoad_Defaults(t *testing.T) {
	c, err := load(t, "[app]\nname = \"Notes\"\nid = \"com.example.notes\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.GogioVersion() != "1.0.0.1" || c.App.Package != "." || c.App.Output != "dist" || c.Web.Port != 8080 {
		t.Errorf("defaults: version %s, package %q, output %q, port %d", c.GogioVersion(), c.App.Package, c.App.Output, c.Web.Port)
	}
	perms, manifest, app := c.AndroidExtras()
	if len(perms) != 1 || perms[0] != "android.permission.POST_NOTIFICATIONS" || manifest != "" || app != "" {
		t.Errorf("default Android extras: %v %q %q; want POST_NOTIFICATIONS only", perms, manifest, app)
	}
}

func TestAndroidExtras_KeepAlive(t *testing.T) {
	c, err := load(t, `
[app]
name = "Agent"
id = "com.example.agent"
[android]
keep_alive = true
notifications = false
permissions = ["network", "camera", "android.permission.VIBRATE", "network"]
application = '<meta-data android:name="x" android:value="y"/>'
`)
	if err != nil {
		t.Fatal(err)
	}
	perms, _, app := c.AndroidExtras()
	want := []string{
		"android.permission.FOREGROUND_SERVICE", "android.permission.FOREGROUND_SERVICE_DATA_SYNC",
		"android.permission.POST_NOTIFICATIONS", "android.permission.INTERNET", "android.permission.CAMERA",
		"android.permission.VIBRATE",
	}
	if strings.Join(perms, " ") != strings.Join(want, " ") {
		t.Errorf("permissions\n got %v\nwant %v", perms, want)
	}
	if !strings.Contains(app, "org.giode.mobile.KeepAliveService") || !strings.Contains(app, `android:foregroundServiceType="dataSync"`) || !strings.Contains(app, "meta-data") {
		t.Errorf("application XML:\n%s", app)
	}
}

func TestIOSPlist(t *testing.T) {
	c, err := load(t, `
[app]
name = "Cam"
id = "com.example.cam"
[ios.usage]
camera = "To attach <photos> & scans."
speech = "To dictate."
[ios.plist]
UIBackgroundModes = ["audio", "fetch"]
ITSAppUsesNonExemptEncryption = false
UIFileSharingEnabled = true
`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.IOSPlist()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<key>NSCameraUsageDescription</key>\n\t<string>To attach &lt;photos&gt; &amp; scans.</string>",
		"<key>NSSpeechRecognitionUsageDescription</key>",
		"<key>UIBackgroundModes</key>\n\t<array><string>audio</string><string>fetch</string></array>",
		"<key>ITSAppUsesNonExemptEncryption</key>\n\t<false/>",
		"<key>UIFileSharingEnabled</key>\n\t<true/>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plist lacks %q:\n%s", want, got)
		}
	}
}

func TestValidate(t *testing.T) {
	for name, src := range map[string]string{
		"missing name":       "[app]\nid = \"com.example.a\"\n",
		"bad id":             "[app]\nname = \"A\"\nid = \"my app\"\n",
		"one-part id":        "[app]\nname = \"A\"\nid = \"app\"\n",
		"bad version":        "[app]\nname = \"A\"\nid = \"com.example.a\"\nversion = \"1.0\"\n",
		"unknown permission": "[app]\nname = \"A\"\nid = \"com.example.a\"\n[android]\npermissions = [\"telepathy\"]\n",
		"unknown usage":      "[app]\nname = \"A\"\nid = \"com.example.a\"\n[ios.usage]\nsmell = \"x\"\n",
		"bad plist value":    "[app]\nname = \"A\"\nid = \"com.example.a\"\n[ios.plist]\nX = { a = 1 }\n",
		"unknown setting":    "[app]\nname = \"A\"\nid = \"com.example.a\"\nicn = \"x.png\"\n",
	} {
		if _, err := load(t, src); err == nil {
			t.Errorf("%s: loaded without an error", name)
		}
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FileName), []byte("[app]\n"), 0o644)
	sub := filepath.Join(dir, "a", "b")
	os.MkdirAll(sub, 0o755)
	p, err := Find(sub)
	if err != nil || p != filepath.Join(dir, FileName) {
		t.Errorf("Find = %q, %v", p, err)
	}
	if _, err := Find(t.TempDir()); err == nil {
		t.Error("found a giode.toml where there's none")
	}
}

func TestAndroidApplicationAttrs(t *testing.T) {
	c, err := load(t, `
[app]
name = "R"
id = "com.example.r"
[android.application_attributes]
requestLegacyExternalStorage = "true"
largeHeap = "true"
`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.AndroidApplicationAttrs(), ` android:largeHeap="true" android:requestLegacyExternalStorage="true"`; got != want {
		t.Errorf("attributes %q, want %q", got, want)
	}
}

func TestFourPartVersionAndBuild(t *testing.T) {
	c, err := load(t, "[app]\nname = \"S\"\nid = \"com.example.s\"\nversion = \"1.2.3.7\"\n")
	if err != nil || c.App.Version != "1.2.3" || c.App.Build != 7 {
		t.Fatalf("four-part app.version: %q build %d, %v", c.App.Version, c.App.Build, err)
	}
	t.Setenv("GIODE_VERSION", "v2.0.1.42")
	c, err = load(t, "[app]\nname = \"S\"\nid = \"com.example.s\"\nversion = \"1.0.0\"\nbuild = 3\n")
	if err != nil || c.App.Version != "2.0.1" || c.App.Build != 42 {
		t.Fatalf("four-part tag: %q build %d, %v", c.App.Version, c.App.Build, err)
	}
	t.Setenv("GIODE_BUILD", "99")
	c, _ = load(t, "[app]\nname = \"S\"\nid = \"com.example.s\"\n")
	if c.App.Build != 99 {
		t.Errorf("$GIODE_BUILD should win over the tag's build: %d", c.App.Build)
	}
	t.Setenv("GIODE_BUILD", "")
	t.Setenv("GIODE_VERSION", "1.0.0.0")
	if _, err := load(t, "[app]\nname = \"S\"\nid = \"com.example.s\"\n"); err == nil || !strings.Contains(err.Error(), "at least 1") {
		t.Errorf("a build of 0 from the tag: %v, want an error", err)
	}
	t.Setenv("GIODE_VERSION", "")
	if _, err := load(t, "[app]\nname = \"S\"\nid = \"com.example.s\"\nbuild = 0\n"); err == nil {
		t.Error("app.build = 0 was accepted")
	}
	if c, err := load(t, "[app]\nname = \"S\"\nid = \"com.example.s\"\n"); err != nil || c.App.Build != 1 {
		t.Errorf("no build set: %d, %v; want the default 1", c.App.Build, err)
	}
	if _, err := load(t, "[app]\nname = \"S\"\nid = \"com.example.s\"\nversion = \"1.2.3.x\"\n"); err == nil {
		t.Error("a non-numeric fourth part was accepted")
	}
}

func TestFirewallPorts(t *testing.T) {
	w := WindowsInstaller{FirewallPorts: []string{"tcp:53317-53318", "UDP:53317"}}
	got, err := w.ParseFirewallPorts()
	if err != nil {
		t.Fatal(err)
	}
	want := []FirewallPort{{"TCP", 53317, 53318}, {"UDP", 53317, 53317}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"53317", "icmp:1", "tcp:0", "tcp:70000", "tcp:20-10", "tcp:x"} {
		if _, err := (WindowsInstaller{FirewallPorts: []string{bad}}).ParseFirewallPorts(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLinuxIDAndScreenshots(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FileName), []byte(`[app]
name = "Easy Send"
id = "app.easysend"
version = "1.0.0"

[linux]
app_id = "io.github.xmhrnx.EasySend"
screenshots = ["https://example.com/a.png", { url = "https://example.com/b.png", caption = "Receiving" }]
`), 0o644)
	c, err := Load(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if c.LinuxID() != "io.github.xmhrnx.EasySend" || c.App.ID != "app.easysend" {
		t.Errorf("LinuxID %q, app.id %q", c.LinuxID(), c.App.ID)
	}
	want := []Screenshot{{URL: "https://example.com/a.png"}, {URL: "https://example.com/b.png", Caption: "Receiving"}}
	if len(c.Linux.Screenshots) != 2 || c.Linux.Screenshots[0] != want[0] || c.Linux.Screenshots[1] != want[1] {
		t.Errorf("screenshots %+v", c.Linux.Screenshots)
	}
	c.Linux.Screenshots = []Screenshot{{URL: "shots/a.png"}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "screenshots") {
		t.Errorf("a screenshot path, not a URL: %v", err)
	}
}
