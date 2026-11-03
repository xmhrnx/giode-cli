package gogio

import (
	"encoding/json"
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
