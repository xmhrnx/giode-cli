package gogio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/xmhrnx/giode-cli/internal/progress"
)

// bundletool builds Android App Bundles. The SDK doesn't ship it, so
// when the build tools folder has none, this version is downloaded
// once into giode's cache and checked against its SHA-256.
const (
	bundletoolVersion = "1.17.2"
	bundletoolSHA256  = "2d4ad908faea64047c1cc9cb747e6aa667c6ab192e09607bd16b67246a8cd6ae"
	bundletoolURL     = "https://github.com/google/bundletool/releases/download/" + bundletoolVersion + "/bundletool-all-" + bundletoolVersion + ".jar"
)

// findBundletool returns a bundletool jar: one in the SDK's build tools
// folder, else the cached download, fetching it the first time.
func findBundletool(buildtools string) (string, error) {
	if found, _ := filepath.Glob(filepath.Join(buildtools, "bundletool*.jar")); len(found) > 0 {
		return found[0], nil
	}
	path, err := BundletoolCachePath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	progress.Logf("downloading bundletool %s (once, for app bundles)", bundletoolVersion)
	if err := download(bundletoolURL, path, bundletoolSHA256); err != nil {
		return "", fmt.Errorf("bundletool isn't in %s and couldn't be downloaded: %w; download it from https://github.com/google/bundletool/releases into that folder", buildtools, err)
	}
	return path, nil
}

// BundletoolCachePath is where findBundletool keeps its download.
func BundletoolCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "giode", "bundletool-"+bundletoolVersion+".jar"), nil
}

// download fetches url into path, checking the file's SHA-256, through
// a temporary file so an interrupted download leaves nothing behind.
func download(url, path, sum string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("%s: checksum %s, want %s", url, got, sum)
	}
	return os.Rename(tmp.Name(), path)
}
