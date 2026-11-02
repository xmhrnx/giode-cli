package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xmhrnx/giode-cli/internal/config"
)

const linksUsage = `
Usage: giode links [options]

Writes the files a website serves to let the app open its links
(app.links in giode.toml) instead of the browser, one directory per host
under dist/links:

	<host>/.well-known/assetlinks.json            Android App Links
	<host>/.well-known/apple-app-site-association iOS and macOS universal links

Upload each host's .well-known directory to its site, served over https
with content type application/json and no redirects.

assetlinks.json lists the SHA-256 fingerprints of the certificates the
Android app may be signed with: android.keystore's (read with keytool,
password from android.keystore_pass_env) and android.fingerprints - add
Play App Signing's, from the Play Console, when Google signs the app.
apple-app-site-association needs ios.team_id.
`

func cmdLinks(args []string) error {
	fs := flags("links", linksUsage)
	dir := fs.String("C", ".", "project directory")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if len(c.App.Links) == 0 {
		return errors.New("giode.toml has no app.links")
	}
	fps := append([]string{}, c.Android.Fingerprints...)
	if c.Android.Keystore != "" {
		fp, err := keystoreFingerprint(c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  note: no fingerprint from android.keystore: %v\n", err)
		} else {
			fps = append(fps, fp)
		}
	}
	if len(fps) == 0 {
		fmt.Fprintln(os.Stderr, "  note: no Android signing fingerprints (android.keystore or android.fingerprints); assetlinks.json lists none")
	}
	out, err := outDir(c, "links")
	if err != nil {
		return err
	}
	for _, host := range c.LinkHosts() {
		wk := filepath.Join(out, host, ".well-known")
		if err := os.MkdirAll(wk, 0o755); err != nil {
			return err
		}
		al, err := c.AssetLinks(fps)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(wk, "assetlinks.json"), al, 0o644); err != nil {
			return err
		}
		if c.IOS.TeamID == "" {
			fmt.Fprintln(os.Stderr, "  note: set ios.team_id for apple-app-site-association")
		} else {
			aasa, err := c.AppleAppSiteAssociation(host, c.IOS.TeamID)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(wk, "apple-app-site-association"), aasa, 0o644); err != nil {
				return err
			}
		}
		fmt.Printf("  %s\n", wk)
	}
	return nil
}

var sha256Line = regexp.MustCompile(`SHA256:\s*([0-9A-F:]{95})`)

// keystoreFingerprint reads the SHA-256 fingerprint of android.keystore's
// certificate with keytool.
func keystoreFingerprint(c *config.Config) (string, error) {
	keytool, err := exec.LookPath("keytool")
	if err != nil {
		return "", errors.New("keytool (from a JDK) isn't on PATH")
	}
	pass := os.Getenv(c.Android.KeystorePassEnv)
	if pass == "" {
		return "", fmt.Errorf("$%s is empty", c.Android.KeystorePassEnv)
	}
	out, err := exec.Command(keytool, "-list", "-v", "-keystore", c.Path(c.Android.Keystore), "-storepass", pass).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("keytool: %v: %s", err, strings.TrimSpace(string(out)))
	}
	m := sha256Line.FindSubmatch(out)
	if m == nil {
		return "", errors.New("keytool printed no SHA256 fingerprint")
	}
	return string(m[1]), nil
}
