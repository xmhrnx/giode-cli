package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestShareTarget(t *testing.T) {
	c := &Config{App: App{Name: "A", ID: "com.example.a", ShareTarget: true}}
	_, _, app := c.AndroidExtras()
	for _, want := range []string{`org.giode.mobile.ShareActivity`, `android.intent.action.SEND"`, `android.intent.action.SEND_MULTIPLE`, `android:mimeType="*/*"`} {
		if !strings.Contains(app, want) {
			t.Errorf("application XML lacks %s:\n%s", want, app)
		}
	}
	plist, err := c.IOSPlist()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<string>group.com.example.a</string>", "<string>giode-share-com.example.a</string>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %s:\n%s", want, plist)
		}
	}
	if g := c.IOSEntitlements()["com.apple.security.application-groups"]; !reflect.DeepEqual(g, []string{"group.com.example.a"}) {
		t.Errorf("app groups = %v", g)
	}
	c.IOS.AppGroup = "group.shared"
	if c.IOSAppGroup() != "group.shared" {
		t.Errorf("app group = %s", c.IOSAppGroup())
	}
	c.IOS.AppGroup = "shared"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "ios.app_group") {
		t.Errorf("Validate = %v, want an app_group error", err)
	}
}

func TestShareTargetOff(t *testing.T) {
	c := &Config{App: App{Name: "A", ID: "com.example.a"}}
	if _, _, app := c.AndroidExtras(); strings.Contains(app, "ShareActivity") {
		t.Error("ShareActivity declared without app.share_target")
	}
	if _, ok := c.IOSEntitlements()["com.apple.security.application-groups"]; ok {
		t.Error("app group without app.share_target")
	}
}
