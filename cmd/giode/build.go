package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "image/jpeg"

	"github.com/xmhrnx/giode-cli/internal/config"
	"github.com/xmhrnx/giode-cli/internal/gogio"
	"github.com/xmhrnx/giode-cli/internal/installer"
	"github.com/xmhrnx/giode-cli/internal/pwa"
	"github.com/xmhrnx/giode-cli/internal/sign"
)

const buildUsage = `
Usage: giode build [options] [target...]

Builds the app in giode.toml for each target, into dist/<platform>. With
no target it builds for this computer's platform, like desktop:

	android   an APK for phones and tablets (dist/android/<App>.apk)
	aab       an Android App Bundle for Google Play (dist/android/<App>.aab)
	ios       an IPA for devices, signed with ios.profile (macOS only)
	ios-sim   an app for the iOS simulator (macOS only)
	macos     a universal (Apple silicon and Intel) macOS app bundle (macOS only)
	dmg       the app in a disk image with a link to Applications (macOS only)
	windows   a Windows executable with icon and manifest (from any OS)
	windows-installer
	          a Windows setup program (<App>-<version>-<arch>-setup.exe) with Start
	          menu shortcut and uninstaller, made with NSIS (from any OS)
	msix      an MSIX package for the Microsoft Store and App Installer
	          (from any OS; installable once signed)
	winget    the setup program and winget manifests for it (from any OS)
	linux     a Linux executable with a .desktop file and icon (Linux only)
	deb, rpm  Debian and RPM packages installing it under /usr, with menu
	          entry, icons and AppStream metadata (Linux only)
	appimage  an AppImage, with appimagetool (Linux only)
	flatpak   a Flatpak bundle with flatpak-builder, or its manifest
	          without it (Linux only)
	web       a WebAssembly site (dist/web)
	desktop   this computer's platform
	all       every target this computer can build

Android and iOS builds add what giode.toml asks for to the manifest and
Info.plist: permissions, giode's background service (android.keep_alive),
usage texts, raw XML and plist keys.

-release builds for distribution, signed with the certificates in
giode.toml: Android with android.keystore, Windows executables and
installers with windows.signing (Authenticode), macOS with
macos.identity (and notarized with macos.notary_id). iOS device builds are
always signed with ios.profile. Without -release, Android uses the debug
key and the rest are unsigned.

Short names work too: apk, bundle, win, mac, sim, installer.

Example:

	giode build android windows
	giode build -release aab windows-installer
`

// buildOpts are the options shared by build and run.
type buildOpts struct {
	release bool
	arch    string
	output  string
	work    bool
	args    []string
}

func cmdBuild(args []string) error {
	fs := flags("build", buildUsage)
	var o buildOpts
	dir := fs.String("C", ".", "project directory")
	fs.BoolVar(&o.release, "release", false, "build for distribution, signed with the certificates in giode.toml")
	fs.StringVar(&o.arch, "arch", "", "comma separated GOARCHs to build (default: the target's set)")
	fs.StringVar(&o.output, "o", "", "output file or directory (one target only)")
	fs.BoolVar(&o.work, "work", false, "keep and print gogio's work directory")
	fs.BoolVar(&verbose, "x", false, "print the commands run")
	targets, err := parse(fs, args)
	if err != nil {
		return err
	}
	c, err := loadProject(*dir)
	if err != nil {
		if len(targets) == 0 {
			fs.Usage()
		}
		return err
	}
	if len(targets) == 0 {
		targets = []string{hostTarget()}
	}
	if o.output != "" && len(targets) > 1 {
		return errors.New("-o works with one target only")
	}
	targets = expandTargets(targets)
	var failed []string
	for _, t := range targets {
		fmt.Printf("Building %s…\n", t)
		start := time.Now()
		out, err := buildTarget(c, t, o)
		if err != nil {
			fmt.Fprintf(os.Stderr, "giode build %s: %v%s\n", t, err, after(start))
			failed = append(failed, t)
			continue
		}
		fmt.Printf("  %s (%s)\n", relPath(out), since(start))
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// targetNames are the targets build accepts, besides desktop and all.
var targetNames = []string{"android", "aab", "ios", "ios-sim", "macos", "dmg", "windows", "windows-installer", "msix", "winget", "linux", "deb", "rpm", "appimage", "flatpak", "web"}

// targetAliases are short and alternative target names.
var targetAliases = map[string]string{
	"apk":           "android",
	"debian":        "deb",
	"AppImage":      "appimage",
	"bundle":        "aab",
	"mac":           "macos",
	"osx":           "macos",
	"darwin":        "macos",
	"win":           "windows",
	"exe":           "windows",
	"installer":     "windows-installer",
	"setup":         "windows-installer",
	"msi":           "windows-installer",
	"sim":           "ios-sim",
	"simulator":     "ios-sim",
	"ios-simulator": "ios-sim",
	"iphone":        "ios",
	"wasm":          "web",
	"js":            "web",
	"browser":       "web",
}

// canonical resolves a target alias.
func canonical(t string) string {
	t = strings.ToLower(t)
	if a, ok := targetAliases[t]; ok {
		return a
	}
	return t
}

// hostTarget is the desktop target for this computer.
func hostTarget() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

func expandTargets(ts []string) []string {
	var out []string
	for _, t := range ts {
		switch t = canonical(t); t {
		case "desktop":
			out = append(out, hostTarget())
		case "all":
			out = append(out, "web", "windows")
			if _, err := exec.LookPath("makensis"); err == nil {
				out = append(out, "windows-installer")
			}
			if runtime.GOOS == "linux" {
				out = append(out, "linux")
			}
			if runtime.GOOS == "darwin" {
				out = append(out, "macos", "ios-sim")
			}
			if androidHome() != "" {
				out = append(out, "android")
			}
		default:
			out = append(out, t)
		}
	}
	return out
}

// buildTarget builds one target and returns the output path.
func buildTarget(c *config.Config, target string, o buildOpts) (string, error) {
	if c.App.Licenses != "" && !licensesWritten[c.Dir] {
		licensesWritten[c.Dir] = true
		if _, err := writeLicenses(c, c.Path(c.App.Licenses)); err != nil {
			return "", fmt.Errorf("app.licenses: %w", err)
		}
	}
	switch target {
	case "linux":
		return buildLinux(c, o)
	case "windows-installer":
		return buildWindowsInstaller(c, o)
	case "deb", "rpm", "appimage", "flatpak":
		return buildLinuxPackage(c, target, o)
	case "msix":
		return buildMSIX(c, o)
	case "dmg":
		return buildDMG(c, o)
	case "winget":
		return buildWinget(c, o)
	case "android", "aab", "ios", "ios-sim", "macos", "windows", "web":
	default:
		return "", fmt.Errorf("unknown target %q; targets: %s, desktop, all", target, strings.Join(targetNames, ", "))
	}
	g, err := gogioOptions(c, target, o)
	if err != nil {
		return "", err
	}
	// gogio runs go in the working directory.
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if err := os.Chdir(c.Dir); err != nil {
		return "", err
	}
	defer os.Chdir(wd)
	gb, err := prepareGio(c, target)
	if err != nil {
		return "", err
	}
	defer gb.cleanup()
	g.ModFile, g.Tags = gb.modfile, gb.tags
	if target == "macos" {
		g.MacBundle = macBundleLibs(c)
	}
	symbols := ""
	if (target == "android" || target == "aab") && o.release {
		// Release builds keep the native libraries' symbols for the Play
		// Console, which symbolizes crash reports with them.
		if symbols, err = os.MkdirTemp("", "giode-symbols-"); err != nil {
			return "", err
		}
		defer os.RemoveAll(symbols)
		g.SymbolsDir = symbols
	}
	if err := gogio.Build(pkgPath(c), g); err != nil {
		return "", err
	}
	if symbols != "" {
		zip := filepath.Join(filepath.Dir(g.Output), "native-debug-symbols.zip")
		if err := zipTree(symbols, zip); err != nil {
			return "", fmt.Errorf("native debug symbols: %w", err)
		}
		fmt.Printf("  %s (upload to the Play Console with the bundle)\n", relPath(zip))
	}
	if target == "web" && (c.Web.PWA == nil || *c.Web.PWA) {
		err := pwa.Make(pwa.App{
			Name: c.App.Name, ShortName: c.Web.ShortName, Description: c.App.Description, ID: c.App.ID,
			Icon: c.Path(c.App.Icon), ThemeColor: c.Web.ThemeColor, BackgroundColor: c.Web.BackgroundColor,
		}, g.Output)
		if err != nil {
			return "", fmt.Errorf("making the PWA: %w", err)
		}
	}
	if target == "windows" {
		if _, err := windowsDLLs(c, g.Output); err != nil {
			return "", err
		}
	}
	if target == "windows" && o.release {
		if err := signWindows(c, g.Output); err != nil {
			return "", err
		}
	}
	return g.Output, nil
}

// signWindows signs a release executable or installer when
// windows.signing is set up, and says so when it isn't.
func signWindows(c *config.Config, file string) error {
	if !c.Windows.Signing.Configured() {
		fmt.Fprintf(os.Stderr, "  note: %s is unsigned; set windows.signing in giode.toml so Windows doesn't warn about it\n", filepath.Base(file))
		return nil
	}
	sign.Verbose = verbose
	if err := sign.Windows(c, file, c.App.Name); err != nil {
		return fmt.Errorf("signing %s: %w", filepath.Base(file), err)
	}
	fmt.Printf("  signed %s\n", filepath.Base(file))
	return nil
}

// buildWindowsInstaller builds the Windows executable and a setup
// program installing it.
func buildWindowsInstaller(c *config.Config, o buildOpts) (string, error) {
	exeOpts := o
	exeOpts.output = ""
	exe, err := buildTarget(c, "windows", exeOpts)
	if err != nil {
		return "", err
	}
	out := filepath.Join(filepath.Dir(exe), fmt.Sprintf("%s-%s-%s-setup.exe", binaryName(c), c.App.Version, windowsArch(c, o)))
	if o.output != "" {
		out, _ = filepath.Abs(o.output)
	}
	inst := c.Windows.Installer
	opts := installer.Options{
		Name: c.App.Name, ID: c.App.ID, Version: c.App.Version,
		Publisher: c.App.Publisher, Website: c.App.Website,
		Exe: exe, Icon: c.Path(c.App.Icon), License: c.Path(inst.License),
		PerMachine: inst.PerMachine, DesktopShortcut: inst.DesktopShortcut,
		Firewall: inst.Firewall, FirewallPublic: inst.FirewallPublic,
		Output: out, Verbose: verbose,
	}
	if opts.Files, err = windowsDLLs(c, exe); err != nil {
		return "", err
	}
	for _, ft := range c.WindowsFileTypes() {
		opts.FileTypes = append(opts.FileTypes, installer.FileType{ProgID: ft.ProgID, Name: ft.Name, Extensions: ft.Extensions, Icon: ft.Icon})
	}
	for _, v := range c.WindowsVerbs() {
		opts.ContextMenu = append(opts.ContextMenu, installer.Verb{Key: v.Key, Label: v.Label, ID: v.ID, Arg: v.Arg})
	}
	if err := installer.Build(opts); err != nil {
		return "", err
	}
	if o.release {
		if err := signWindows(c, out); err != nil {
			return "", err
		}
	}
	return out, nil
}

// gogioOptions turns giode.toml into gogio options for a target.
func gogioOptions(c *config.Config, target string, o buildOpts) (gogio.Options, error) {
	g := gogio.Options{
		AppID:         c.App.ID,
		Name:          binaryName(c),
		Version:       c.GogioVersion(),
		Tags:          c.App.Tags,
		Ldflags:       desktopLdflags(c, target),
		Schemes:       c.App.Schemes,
		Arch:          o.arch,
		PrintCommands: verbose,
		KeepWorkdir:   o.work,
		Args:          o.args,
	}
	if icon := c.Path(c.App.Icon); icon != "" {
		if err := checkIcon(icon); err != nil {
			return g, err
		}
		g.Icon = icon
	}
	var platform, file string
	switch target {
	case "android", "aab":
		if err := ensureAndroidHome(); err != nil {
			return g, err
		}
		g.Target = "android"
		g.MinSDK, g.TargetSDK = c.Android.MinSDK, c.Android.TargetSDK
		g.Queries = c.Android.Queries
		if g.Arch == "" && len(c.Android.Arch) > 0 {
			g.Arch = strings.Join(c.Android.Arch, ",")
		}
		perms, manifest, app := c.AndroidExtras()
		g.Extras = gogio.Extras{AndroidPermissions: perms, AndroidManifest: manifest, AndroidApplication: app, AndroidApplicationAttrs: c.AndroidApplicationAttrs(), AndroidActivity: c.AndroidActivityXML(),
			SplashColor: c.App.SplashColor, SplashColorLight: c.App.SplashColorLight}
		values, err := c.GoogleServicesXML()
		if err != nil {
			return g, err
		}
		g.Extras.AndroidValues = values
		g.AndroidDependencies, g.AndroidRepos = c.Android.Dependencies, c.Android.Repositories
		for _, l := range c.Android.Libraries {
			g.AndroidLibs = append(g.AndroidLibs, c.Path(l))
		}
		java, err := javaSources(c)
		if err != nil {
			return g, err
		}
		g.Extras.AndroidJava = java
		g.Extras.AndroidIcon = gogio.AdaptiveIcon{
			Foreground: pathOrEmpty(c, c.Android.IconForeground),
			Background: c.Android.IconBackground,
			Monochrome: pathOrEmpty(c, c.Android.IconMonochrome),
		}
		if bg := c.Android.IconBackground; bg != "" && !strings.HasPrefix(bg, "#") {
			g.Extras.AndroidIcon.Background = c.Path(bg)
		}
		if o.release {
			if c.Android.Keystore == "" {
				return g, errors.New("-release needs android.keystore in giode.toml (giode keystore creates one)")
			}
			g.SignKey = c.Path(c.Android.Keystore)
			g.SignPass = os.Getenv(c.Android.KeystorePassEnv)
			if g.SignPass == "" {
				return g, fmt.Errorf("-release needs the keystore password in $%s", c.Android.KeystorePassEnv)
			}
		}
		platform, file = "android", binaryName(c)+".apk"
		if target == "aab" {
			file = binaryName(c) + ".aab"
		}
	case "ios", "ios-sim":
		if runtime.GOOS != "darwin" {
			return g, errors.New("iOS builds need macOS and Xcode")
		}
		g.Target = "ios"
		g.MinSDK = c.IOS.MinOS
		if g.MinSDK == 0 {
			g.MinSDK = 15
		}
		plist, err := c.IOSPlist()
		if err != nil {
			return g, err
		}
		privacy, err := c.PrivacyManifest()
		if err != nil {
			return g, err
		}
		g.Extras = gogio.Extras{InfoPlist: plist, PrivacyManifest: privacy, Entitlements: c.IOSEntitlements(),
			SplashColor: c.App.SplashColor, SplashColorLight: c.App.SplashColorLight}
		if c.App.ShareTarget {
			ext := &gogio.ShareExtension{AppGroup: c.IOSAppGroup(), Scheme: c.IOSShareScheme()}
			if target == "ios" {
				if c.IOS.ShareProfile == "" {
					return g, errors.New("device builds with app.share_target need ios.share_profile, a provisioning profile for the share extension (bundle ID " + c.App.ID + ".share, App Group " + c.IOSAppGroup() + ")")
				}
				ext.Profile = c.Path(c.IOS.ShareProfile)
			}
			g.Extras.ShareExtension = ext
			g.Schemes = append(append([]string(nil), g.Schemes...), c.IOSShareScheme())
		}
		platform, file = "ios", binaryName(c)+".ipa"
		if target == "ios-sim" {
			// gogio builds simulator apps for amd64; Xcode runs them on
			// Apple silicon too.
			if g.Arch == "" {
				g.Arch = "amd64"
			}
			file = binaryName(c) + ".app"
		} else {
			if c.IOS.Profile == "" {
				return g, errors.New("device builds need ios.profile, a provisioning profile, in giode.toml; use ios-sim for the simulator")
			}
			g.SignKey = c.Path(c.IOS.Profile)
		}
	case "macos":
		if runtime.GOOS != "darwin" {
			return g, errors.New("macOS app bundles need macOS")
		}
		g.Target = "macos"
		g.MinSDK = c.MacOS.MinOS
		plist, err := c.MacOSPlist()
		if err != nil {
			return g, err
		}
		privacy, err := c.PrivacyManifest()
		if err != nil {
			return g, err
		}
		g.Extras = gogio.Extras{InfoPlist: plist, PrivacyManifest: privacy, Entitlements: c.MacOSEntitlements()}
		if o.release {
			switch {
			case c.MacOS.Profile != "":
				g.SignKey = c.Path(c.MacOS.Profile)
			case c.MacOS.Identity != "":
				g.SignKey = c.MacOS.Identity
			default:
				fmt.Fprintln(os.Stderr, "  note: the app is unsigned; set macos.identity in giode.toml so Gatekeeper lets it open")
			}
			if c.MacOS.NotaryID != "" {
				if g.SignKey == "" {
					return g, errors.New("notarizing needs a signed app: set macos.identity")
				}
				g.NotaryID, g.NotaryTeamID = c.MacOS.NotaryID, c.MacOS.NotaryTeamID
				g.NotaryPass = os.Getenv(c.MacOS.NotaryPassEnv)
			}
		}
		platform, file = "macos", binaryName(c)+".app"
	case "windows":
		g.Target = "windows"
		if g.Arch == "" {
			g.Arch = c.Windows.Arch
		}
		if g.Arch == "" {
			g.Arch = "amd64"
		}
		g.Env = c.WindowsCgoEnv()
		platform, file = "windows", binaryName(c)+".exe"
	case "web":
		g.Target = "js"
		platform, file = "web", ""
	}
	if o.output != "" {
		g.Output = o.output
		if !filepath.IsAbs(g.Output) {
			wd, _ := os.Getwd()
			g.Output = filepath.Join(wd, g.Output)
		}
		return g, nil
	}
	d, err := outDir(c, platform)
	if err != nil {
		return g, err
	}
	g.Output = filepath.Join(d, file)
	if target == "windows" && g.Arch != "amd64" {
		g.Output = filepath.Join(d, binaryName(c)+"-"+g.Arch+".exe")
	}
	if target == "web" {
		g.Output = d
	}
	return g, nil
}

// checkIcon reports an icon the stores and launchers would reject.
func checkIcon(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("app.icon: %w", err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("app.icon %s: %w", path, err)
	}
	if cfg.Width != cfg.Height {
		return fmt.Errorf("app.icon %s is %dx%d; it must be square", path, cfg.Width, cfg.Height)
	}
	if cfg.Width < 512 {
		fmt.Fprintf(os.Stderr, "giode: app.icon %s is %dx%d; stores want at least 512x512 (1024x1024 for the App Store)\n", path, cfg.Width, cfg.Height)
	}
	return nil
}

// buildLinux builds a Linux executable with a .desktop file and icon.
// Gio needs cgo on Linux, so this builds on Linux only.
func buildLinux(c *config.Config, o buildOpts) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("Linux builds need a Linux computer (Gio uses cgo for Wayland and X11); build in a Linux VM, container or CI")
	}
	d, err := outDir(c, "linux")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(d, strings.ToLower(binaryName(c)))
	if o.output != "" {
		bin, _ = filepath.Abs(o.output)
		d = filepath.Dir(bin)
	}
	ldflags := strings.TrimSpace("-s -w -X gioui.org/app.ID=" + c.App.ID + " " + desktopLdflags(c, "linux") + " " + linuxRpathLdflag(c, filepath.Base(bin)))
	gb, err := prepareGio(c, "linux")
	if err != nil {
		return "", err
	}
	defer gb.cleanup()
	args := append([]string{"build", "-trimpath", "-ldflags", ldflags, "-o", bin}, gb.goArgs()...)
	cmd := command(c, "go", append(args, pkgPath(c))...)
	if o.arch != "" {
		cmd.Env = append(os.Environ(), "GOARCH="+o.arch)
	}
	if err := run(cmd); err != nil {
		return "", err
	}
	libs, err := nativeLibs(c, "linux", bin)
	if err != nil {
		return "", err
	}
	if _, err := copyLibs(libs, filepath.Join(d, "lib")); err != nil {
		return "", err
	}
	if icon := c.Path(c.App.Icon); icon != "" {
		if err := writeLinuxIcon(icon, filepath.Join(d, c.App.ID+".png")); err != nil {
			return "", err
		}
	}
	cats := "Utility;"
	if len(c.Linux.Categories) > 0 {
		cats = strings.Join(c.Linux.Categories, ";") + ";"
	}
	desktop := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s\nExec=%s %%U\nIcon=%s\nCategories=%s\nTerminal=false\n",
		c.App.Name, filepath.Base(bin), c.App.ID, cats)
	types, _ := c.LinuxMIMETypes(filepath.Base(bin))
	for _, s := range c.App.Schemes {
		types = append(types, "x-scheme-handler/"+s)
	}
	if len(types) > 0 {
		desktop += "MimeType=" + strings.Join(types, ";") + ";\n"
	}
	if err := os.WriteFile(filepath.Join(d, c.App.ID+".desktop"), []byte(desktop), 0o644); err != nil {
		return "", err
	}
	return bin, nil
}

// writeLinuxIcon copies the icon as PNG, as desktop environments
// expect.
func writeLinuxIcon(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	err = png.Encode(out, img)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// desktopLdflags returns the linker flags for a target: app.ldflags,
// plus on desktops the context actions and update settings.
func desktopLdflags(c *config.Config, target string) string {
	flags := versionLdflags(c) + " " + strings.NewReplacer(
		"{version}", c.App.Version,
		"{build}", strconv.Itoa(c.App.Build),
		"{id}", c.App.ID,
	).Replace(c.App.Ldflags)
	switch target {
	case "windows", "macos", "linux":
		flags += " " + c.ContextActionsLdflag() + " " + c.UpdateLdflags()
	}
	return strings.Join(withLegacyGiodePath(strings.Fields(flags)), " ")
}

// giodeModule is the giode library's module path; legacyGiodeModule
// the one it had before the GitHub account was renamed, which apps not
// yet moved with giode upgrade still import.
const (
	giodeModule       = "github.com/xmhrnx/giode"
	legacyGiodeModule = "github.com/mehran9675/giode"
)

// withLegacyGiodePath repeats each -X setting of a giode variable under
// the legacy module path, so apps that still import it get them too:
// the linker ignores -X for a package the program doesn't contain.
func withLegacyGiodePath(fields []string) []string {
	out := fields
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != "-X" {
			continue
		}
		v := fields[i+1]
		if rest, ok := strings.CutPrefix(v, giodeModule); ok && (strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, "/")) {
			out = append(out, "-X", legacyGiodeModule+rest)
		}
	}
	return out
}

// versionLdflags sets giode.AppVersion and giode.AppBuild, so the app
// can show the version it was built as (the release tag in CI, through
// $GIODE_VERSION), and giode.AppID and the app name, which name its
// login entry. Apps
// on a giode without them link as before: -X ignores missing
// variables.
func versionLdflags(c *config.Config) string {
	flags := fmt.Sprintf("-X github.com/xmhrnx/giode.AppVersion=%s -X github.com/xmhrnx/giode.AppBuild=%d", c.App.Version, c.App.Build)
	if c.App.ID != "" && !strings.ContainsAny(c.App.ID, " \t'\"") {
		flags += " -X github.com/xmhrnx/giode.AppID=" + c.App.ID
	}
	if c.App.Name != "" {
		// Base64, as names have spaces that would split -ldflags.
		flags += " -X github.com/xmhrnx/giode.buildAppName=" + base64.URLEncoding.EncodeToString([]byte(c.App.Name))
	}
	return flags
}

// licensesWritten records projects whose app.licenses this run already
// refreshed.
var licensesWritten = map[string]bool{}

// javaSources returns the .java files android.java names: files, and
// directories searched recursively.
func javaSources(c *config.Config) ([]string, error) {
	var files []string
	for _, p := range c.Android.Java {
		root := c.Path(p)
		st, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("android.java: %w", err)
		}
		if !st.IsDir() {
			files = append(files, root)
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".java") {
				files = append(files, path)
			}
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("android.java: %w", err)
		}
	}
	return files, nil
}

// pathOrEmpty resolves a giode.toml path, keeping "" unset.
func pathOrEmpty(c *config.Config, p string) string {
	if p == "" {
		return ""
	}
	return c.Path(p)
}
