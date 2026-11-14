package gogio

import (
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAndroidThemesSplash(t *testing.T) {
	defer func(e Extras) { extras = e }(extras)
	extras = Extras{}
	plain := androidThemes(true)
	if !strings.Contains(plain["values/themes.xml"], "@android:color/white") || len(plain) != 2 {
		t.Fatalf("without a splash colour: %v", plain)
	}
	extras = Extras{SplashColor: "#0A0A0B", SplashColorLight: "#F4F4F6"}
	f := androidThemes(true)
	if !strings.Contains(f["values/giode_splash.xml"], ">#F4F4F6<") || !strings.Contains(f["values-night/giode_splash.xml"], ">#0A0A0B<") {
		t.Errorf("colours: %q / %q", f["values/giode_splash.xml"], f["values-night/giode_splash.xml"])
	}
	for _, name := range []string{"values/themes.xml", "values-v21/themes.xml", "values-v31/themes.xml"} {
		if !strings.Contains(f[name], `<item name="android:windowBackground">@color/giode_splash</item>`) {
			t.Errorf("%s doesn't use the splash colour:\n%s", name, f[name])
		}
	}
	v31 := f["values-v31/themes.xml"]
	if !strings.Contains(v31, "windowSplashScreenBackground\">@color/giode_splash") || !strings.Contains(v31, "windowSplashScreenAnimatedIcon\">@mipmap/ic_launcher") {
		t.Errorf("Android 12 splash:\n%s", v31)
	}
	if strings.Contains(androidThemes(false)["values-v31/themes.xml"], "AnimatedIcon") {
		t.Error("splash icon without a launcher icon")
	}
}

func TestColorSetJSON(t *testing.T) {
	js, err := colorSetJSON("#0A0A0B", "#F4F4F6")
	if err != nil {
		t.Fatal(err)
	}
	var cs struct {
		Colors []struct {
			Appearances []map[string]string
			Color       struct {
				Components map[string]string
			}
		}
	}
	if err := json.Unmarshal(js, &cs); err != nil {
		t.Fatal(err)
	}
	if len(cs.Colors) != 2 || cs.Colors[0].Color.Components["red"] != "0xF4" || cs.Colors[1].Appearances[0]["value"] != "dark" || cs.Colors[1].Color.Components["blue"] != "0x0B" {
		t.Errorf("colour set:\n%s", js)
	}
	if _, err := colorSetJSON("black", ""); err == nil {
		t.Error("accepted a colour name")
	}
}

// A launch image becomes an image set, the source as @3x, and the
// launch screen's UIImageName.
func TestLaunchImage(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "logo.png")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 600, 300))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	set := filepath.Join(dir, launchImageName+".imageset")
	if err := launchImageSet(set, src); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]image.Point{"launch@1x.png": {200, 100}, "launch@2x.png": {400, 200}, "launch@3x.png": {600, 300}} {
		f, err := os.Open(filepath.Join(set, name))
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(f)
		f.Close()
		if err != nil || (image.Point{cfg.Width, cfg.Height}) != want {
			t.Errorf("%s: %dx%d, %v; want %v", name, cfg.Width, cfg.Height, err, want)
		}
	}
	js, err := os.ReadFile(filepath.Join(set, "Contents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cs struct{ Images []map[string]string }
	if err := json.Unmarshal(js, &cs); err != nil || len(cs.Images) != 3 || cs.Images[2]["scale"] != "3x" || cs.Images[2]["filename"] != "launch@3x.png" {
		t.Errorf("Contents.json %v:\n%s", err, js)
	}

	defer func(e Extras) { extras = e }(extras)
	bi := &buildInfo{name: "app", appID: "com.example.app", target: "ios", minsdk: 15}
	extras = Extras{LaunchImage: src}
	plist := buildInfoPlist(bi)
	if !strings.Contains(plist, "<key>UIImageName</key>\n\t\t<string>"+launchImageName+"</string>") || strings.Contains(plist, "UIColorName") || strings.Contains(plist, "UILaunchStoryboardName") {
		t.Errorf("image only:\n%s", plist)
	}
	extras = Extras{LaunchImage: src, SplashColor: "#0A0A0B"}
	if plist := buildInfoPlist(bi); !strings.Contains(plist, "UIColorName") || !strings.Contains(plist, "UIImageName") {
		t.Errorf("image and colour:\n%s", plist)
	}
	extras = Extras{}
	if plist := buildInfoPlist(bi); strings.Contains(plist, "UIImageName") || !strings.Contains(plist, "<key>UILaunchScreen</key>\n\t<true/>") {
		t.Errorf("neither:\n%s", plist)
	}
}
