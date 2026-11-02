package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const devicesUsage = `
Usage: giode devices

Lists connected Android devices and emulators, and on macOS the booted
iOS simulators, with the IDs giode run -device takes.
`

func cmdDevices(args []string) error {
	fs := flags("devices", devicesUsage)
	if _, err := parse(fs, args); err != nil {
		return err
	}
	fmt.Println("Android:")
	if adbPath, err := adb(); err != nil {
		fmt.Println("  ", err)
	} else if out, err := output(adbPath, "devices", "-l"); err != nil {
		fmt.Println("  ", err)
	} else {
		lines := strings.Split(out, "\n")[1:]
		if len(lines) == 0 || strings.TrimSpace(strings.Join(lines, "")) == "" {
			fmt.Println("   none connected (turn on USB debugging, or start an emulator)")
		}
		for _, l := range lines {
			if l = strings.TrimSpace(l); l != "" {
				fmt.Println("  ", l)
			}
		}
	}
	if runtime.GOOS == "darwin" {
		fmt.Println("iOS simulators (booted):")
		out, err := output("xcrun", "simctl", "list", "devices", "booted")
		if err != nil {
			fmt.Println("  ", err)
		}
		n := 0
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "Booted") {
				fmt.Println("  ", strings.TrimSpace(l))
				n++
			}
		}
		if n == 0 {
			fmt.Println("   none booted (open Simulator.app)")
		}
	}
	return nil
}

const keystoreUsage = `
Usage: giode keystore [options]

Creates an Android release keystore with keytool and points
android.keystore in giode.toml at it. Keep the file and its password
safe: Google Play only accepts updates signed with the same key (or, with
Play App Signing, the same upload key). The password is read from
$GIODE_KEYSTORE_PASS (or android.keystore_pass_env).
`

func cmdKeystore(args []string) error {
	fs := flags("keystore", keystoreUsage)
	dir := fs.String("C", ".", "project directory")
	out := fs.String("o", "release.keystore", "keystore file")
	alias := fs.String("alias", "release", "key alias")
	dname := fs.String("dname", "", `certificate subject (default "CN=<app name>")`)
	years := fs.Int("years", 30, "validity in years")
	fs.BoolVar(&verbose, "x", false, "print the commands run")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	pass := os.Getenv(c.Android.KeystorePassEnv)
	if len(pass) < 6 {
		return fmt.Errorf("set $%s to the keystore password (at least 6 characters)", c.Android.KeystorePassEnv)
	}
	path := c.Path(*out)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	keytool, err := exec.LookPath("keytool")
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		keytool, err = filepath.Join(jh, "bin", "keytool"+exe()), nil
	}
	if err != nil {
		return errors.New("keytool wasn't found: install a JDK (giode doctor)")
	}
	if *dname == "" {
		*dname = "CN=" + strings.ReplaceAll(c.App.Name, ",", "")
	}
	cmd := command(c, keytool, "-genkeypair", "-keystore", path, "-alias", *alias,
		"-keyalg", "RSA", "-keysize", "2048", "-validity", fmt.Sprint(*years*365),
		"-storepass:env", c.Android.KeystorePassEnv, "-keypass:env", c.Android.KeystorePassEnv,
		"-dname", *dname)
	if err := run(cmd); err != nil {
		return err
	}
	if c.Android.Keystore == "" {
		if err := setKeystore(filepath.Join(c.Dir, "giode.toml"), *out); err != nil {
			return fmt.Errorf("created %s, but couldn't update giode.toml: %w", path, err)
		}
		fmt.Printf("Created %s and set android.keystore in giode.toml.\n", relPath(path))
	} else {
		fmt.Printf("Created %s. giode.toml's android.keystore still points at %s.\n", relPath(path), c.Android.Keystore)
	}
	fmt.Println("Build signed packages with: giode build -release aab")
	return nil
}

// setKeystore points android.keystore at path.
func setKeystore(tomlPath, keystore string) error {
	return setTOMLValue(tomlPath, "android", "keystore", filepath.ToSlash(keystore))
}

const upgradeUsage = `
Usage: giode upgrade [version]

Updates the project's giode dependency to the latest release (or the
given version) and tidies go.mod, bringing Gio along at the version giode
needs. A project still on the old module path, github.com/mehran9675/giode,
is moved to github.com/xmhrnx/giode: its imports are rewritten.
`

func cmdUpgrade(args []string) error {
	fs := flags("upgrade", upgradeUsage)
	dir := fs.String("C", ".", "project directory")
	fs.BoolVar(&verbose, "x", false, "print the commands run")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	version := "latest"
	if len(pos) > 0 {
		version = pos[0]
	}
	before, _ := output("go", "-C", *dir, "list", "-m", "-f", "{{.Version}}", giodeModule)
	if legacy, err := output("go", "-C", *dir, "list", "-m", "-f", "{{.Version}}", legacyGiodeModule); err == nil && strings.TrimSpace(legacy) != "" {
		before = legacy
		n, err := moveGiodeImports(*dir)
		if err != nil {
			return err
		}
		fmt.Printf("Moved %d files from %s to %s.\n", n, legacyGiodeModule, giodeModule)
		if err := run(command(nil, "go", "-C", *dir, "mod", "edit", "-droprequire="+legacyGiodeModule)); err != nil {
			return err
		}
	}
	if err := run(command(nil, "go", "-C", *dir, "get", "github.com/xmhrnx/giode@"+version)); err != nil {
		return err
	}
	if err := run(command(nil, "go", "-C", *dir, "mod", "tidy")); err != nil {
		return err
	}
	after, _ := output("go", "-C", *dir, "list", "-m", "-f", "{{.Version}}", giodeModule)
	if before == after {
		fmt.Printf("giode is up to date (%s).\n", after)
	} else {
		fmt.Printf("giode %s → %s\n", before, after)
	}
	return nil
}

// moveGiodeImports rewrites imports of the legacy giode module path to
// the current one in the project's Go files, skipping hidden
// directories and vendor, and reports how many files changed.
func moveGiodeImports(dir string) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		moved := strings.NewReplacer(
			`"`+legacyGiodeModule+`"`, `"`+giodeModule+`"`,
			`"`+legacyGiodeModule+`/`, `"`+giodeModule+`/`,
		).Replace(string(b))
		if moved == string(b) {
			return nil
		}
		n++
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(moved), info.Mode().Perm())
	})
	return n, err
}

const manifestUsage = `
Usage: giode manifest [android|ios|macos]

Prints what giode.toml adds to the Android manifest and the iOS and
macOS Info.plist, on top of what gogio generates, to check permissions
and usage texts before a build.
`

func cmdManifest(args []string) error {
	fs := flags("manifest", manifestUsage)
	dir := fs.String("C", ".", "project directory")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	which := map[string]bool{"android": true, "ios": true, "macos": true}
	if len(pos) > 0 {
		which = map[string]bool{}
		for _, p := range pos {
			which[p] = true
		}
	}
	if which["android"] {
		perms, manifest, app := c.AndroidExtras()
		fmt.Println("Android manifest additions:")
		for _, p := range perms {
			fmt.Printf("\t<uses-permission android:name=%q/>\n", p)
		}
		if manifest != "" {
			fmt.Println(indent(manifest))
		}
		if attrs := c.AndroidApplicationAttrs(); attrs != "" {
			fmt.Printf("\t<application%s>\n", attrs)
		}
		if app != "" {
			fmt.Println("\tin <application>:")
			fmt.Println(indent(app))
		}
		fmt.Println()
	}
	for _, p := range []struct {
		name string
		fn   func() (string, error)
	}{{"ios", c.IOSPlist}, {"macos", c.MacOSPlist}} {
		if !which[p.name] {
			continue
		}
		s, err := p.fn()
		if err != nil {
			return err
		}
		fmt.Printf("%s Info.plist additions:\n", map[string]string{"ios": "iOS", "macos": "macOS"}[p.name])
		if s == "" {
			fmt.Println("\t(none)")
		} else {
			fmt.Println(indent(s))
		}
		fmt.Println()
	}
	return nil
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "\t" + strings.TrimLeft(l, "\t")
	}
	return strings.Join(lines, "\n")
}

const cleanUsage = `
Usage: giode clean

Removes the build output directory (app.output, dist by default).
`

func cmdClean(args []string) error {
	fs := flags("clean", cleanUsage)
	dir := fs.String("C", ".", "project directory")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	out := c.Path(c.App.Output)
	if filepath.Clean(out) == filepath.Clean(c.Dir) {
		return errors.New("app.output is the project directory; refusing to delete it")
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	fmt.Println("Removed", relPath(out))
	return nil
}
