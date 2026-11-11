package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xmhrnx/giode-cli/internal/config"
	"github.com/xmhrnx/giode-cli/internal/gogio"
)

func writeIcon(t *testing.T, dir, name string, alpha uint8) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.SetNRGBA(0, 0, color.NRGBA{A: alpha})
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

// iOS takes ios.icon, which must be opaque, over app.icon.
func TestIOSIcon(t *testing.T) {
	dir := t.TempDir()
	app := writeIcon(t, dir, "icon.png", 0)
	ios := writeIcon(t, dir, "icon-ios.png", 0xff)
	c := &config.Config{Dir: dir}

	g := gogio.Options{Icon: app}
	if err := iosIcon(c, &g); err != nil || g.Icon != app {
		t.Errorf("without ios.icon: icon %s, err %v", g.Icon, err)
	}
	c.IOS.Icon = "icon-ios.png"
	if err := iosIcon(c, &g); err != nil || g.Icon != ios {
		t.Errorf("with ios.icon: icon %s, err %v", g.Icon, err)
	}
	c.IOS.Icon = "icon.png"
	if err := iosIcon(c, &g); err == nil || !strings.Contains(err.Error(), "transparent") {
		t.Errorf("a transparent ios.icon: err %v", err)
	}
}
