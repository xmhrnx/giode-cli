package gogio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLatestPlatform(t *testing.T) {
	sdk := t.TempDir()
	for _, name := range []string{"android-34", "android-34-ext12", "android-36.1", "android-37.0", "android-CinnamonBun", "android-38"} {
		dir := filepath.Join(sdk, "platforms", name)
		os.MkdirAll(dir, 0o755)
		if name != "android-38" { // a broken install without android.jar
			os.WriteFile(filepath.Join(dir, "android.jar"), nil, 0o644)
		}
	}
	got, err := latestPlatform(sdk)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "android-37.0" {
		t.Errorf("latest platform %s, want android-37.0", filepath.Base(got))
	}
	if _, err := latestPlatform(t.TempDir()); err == nil {
		t.Error("no error without platforms")
	}
}
