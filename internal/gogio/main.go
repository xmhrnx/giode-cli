// SPDX-License-Identifier: Unlicense OR MIT
//
// Derived from gioui.org/cmd/gogio (v0.10.3); see LICENSE in this directory.

package gogio

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/sync/errgroup"

	"github.com/xmhrnx/giode-cli/internal/progress"
)

// The build settings, set by Build from its Options. They're package
// variables because the code derived from gogio reads them that way.
var (
	target        = new(string)
	archNames     = new(string)
	minsdk        = new(int)
	targetsdk     = new(int)
	buildMode     = new(string)
	destPath      = new(string)
	appID         = new(string)
	name          = new(string)
	version       = new(string)
	printCommands = new(bool)
	keepWorkdir   = new(bool)
	linkMode      = new(string)
	extraLdflags  = new(string)
	extraTags     = new(string)
	iconPath      = new(string)
	signKey       = new(string)
	signPass      = new(string)
	notaryID      = new(string)
	notaryPass    = new(string)
	notaryTeamID  = new(string)
	schemes       = new(string)
	pkgQueries    = new(string)
	appArgs       []string
	extras        Extras
)

// Options configures Build; the fields match gogio's flags.
type Options struct {
	// Target is ios, tvos, android, js, windows or macos.
	Target string
	// Arch is a comma separated list of GOARCHs; empty for the
	// target's default.
	Arch      string
	MinSDK    int
	TargetSDK int
	// BuildMode is exe (default) or archive.
	BuildMode string
	// Output is the output file or directory. For ios or tvos, a .app
	// suffix builds for the simulator.
	Output string
	AppID  string
	Name   string
	// Version is major.minor.patch.versioncode; default 1.0.0.1.
	Version string
	// PrintCommands prints the commands run; KeepWorkdir keeps the
	// temporary directory.
	PrintCommands bool
	KeepWorkdir   bool
	LinkMode      string
	Ldflags       string
	Tags          string
	Icon          string
	// SignKey is a keystore (Android) or provisioning profile (macOS,
	// iOS); SignPass its password.
	SignKey      string
	SignPass     string
	NotaryID     string
	NotaryPass   string
	NotaryTeamID string
	Schemes      []string
	Queries      []string
	// Args are appended to os.Args at runtime.
	Args []string
	// Extras adds what gogio's templates can't express.
	Extras Extras
	// SymbolsDir, when set, receives the unstripped native libraries of
	// Android builds, by ABI, for the Play Console's crash reports.
	SymbolsDir string
	// ModFile, when set, is passed as -modfile to the go build and go
	// list commands run in the app's module - and only to those, since
	// go refuses the flag outside module mode.
	ModFile string
	// AndroidLibs are local .aar and .jar files built into Android
	// apps; AndroidDependencies are Maven coordinates
	// (group:artifact:version) resolved, with their dependencies, from
	// AndroidRepos (maven.DefaultRepos when empty) into MavenCache.
	// MacBundle, when set, runs on a macOS app bundle after it's built
	// and before it's signed: bundling libraries into it.
	MacBundle func(app string) error
	// Env is added to the environment of the go build of Windows
	// programs: CGO_ENABLED and CC for cgo apps.
	Env []string

	AndroidLibs         []string
	AndroidDependencies []string
	AndroidRepos        []string
	MavenCache          string
}

// modFile is Options.ModFile for the build in progress, buildEnv its
// Options.Env.
var (
	modFile   string
	buildEnv  []string
	macBundle func(app string) error
)

// goCommand returns a go build or go list command run in the app's
// module, with -modfile when the build uses one.
func goCommand(sub string, args ...string) *exec.Cmd {
	a := []string{sub}
	if modFile != "" {
		a = append(a, "-modfile="+modFile)
	}
	return exec.Command("go", append(a, args...)...)
}

// Extras are additions to the generated app metadata, beyond gogio.
type Extras struct {
	// AndroidPermissions are extra <uses-permission> names
	// (android.permission.X).
	AndroidPermissions []string
	// AndroidManifest is XML added inside <manifest>, and
	// AndroidApplication XML added inside <application>.
	AndroidManifest    string
	AndroidApplication string
	// AndroidIcon holds the adaptive icon's layers (Android 8+); see
	// AdaptiveIcon. Unset layers come from the app icon.
	AndroidIcon AdaptiveIcon
	// AndroidJava are the app's own .java files, compiled with Gio's
	// against android.jar and added to the APK.
	AndroidJava []string
	// AndroidApplicationAttrs are attributes added to <application>,
	// each with a leading space: ` android:requestLegacyExternalStorage="true"`.
	AndroidApplicationAttrs string
	// InfoPlist is XML key/value pairs added to the iOS, tvOS and
	// macOS Info.plist dictionary.
	InfoPlist string
	// PrivacyManifest is the PrivacyInfo.xcprivacy file written into
	// iOS and macOS app bundles.
	PrivacyManifest []byte
	// AndroidActivity is XML added inside the app's <activity>: intent
	// filters for App Links.
	AndroidActivity string
	// Entitlements replace or add entitlements of iOS (and macOS
	// provisioning profile) signing: strings, booleans and lists of
	// strings, by key.
	Entitlements map[string]any
	// AndroidValues is a res/values resource file added to Android
	// apps: Firebase's google-services strings.
	AndroidValues string
	// SplashColor is the background ("#RRGGBB") shown before the first
	// frame: Android's window background and Android 12+'s splash
	// screen, and iOS's launch screen. SplashColorLight, when set, is
	// used instead in light mode. Empty keeps white.
	SplashColor, SplashColorLight string
	// ShareExtension, when set, builds a share extension into iOS apps.
	ShareExtension *ShareExtension
}

// Build builds and packages the Go package pkg as gogio would.
func Build(pkg string, o Options) error {
	*target, *archNames, *minsdk, *targetsdk = o.Target, o.Arch, o.MinSDK, o.TargetSDK
	*buildMode, *destPath, *appID, *name = o.BuildMode, o.Output, o.AppID, o.Name
	*version, *printCommands, *keepWorkdir = o.Version, o.PrintCommands, o.KeepWorkdir
	*linkMode, *extraLdflags, *extraTags, *iconPath = o.LinkMode, o.Ldflags, o.Tags, o.Icon
	*signKey, *signPass = o.SignKey, o.SignPass
	*notaryID, *notaryPass, *notaryTeamID = o.NotaryID, o.NotaryPass, o.NotaryTeamID
	*schemes, *pkgQueries = strings.Join(o.Schemes, ","), strings.Join(o.Queries, ",")
	appArgs, extras = o.Args, o.Extras
	modFile, symbolsDir, buildEnv, macBundle = o.ModFile, o.SymbolsDir, o.Env, o.MacBundle
	androidLibs, androidDeps, androidRepos, mavenCache = o.AndroidLibs, o.AndroidDependencies, o.AndroidRepos, o.MavenCache
	defer func() {
		modFile, symbolsDir, buildEnv, macBundle = "", "", nil, nil
		androidLibs, androidDeps, androidRepos, mavenCache = nil, nil, nil, ""
	}()
	if *buildMode == "" {
		*buildMode = "exe"
	}
	if *version == "" {
		*version = "1.0.0.1"
	}
	if err := validate(pkg); err != nil {
		return err
	}
	bi, err := newBuildInfo(pkg)
	if err != nil {
		return err
	}
	return build(bi)
}

func validate(pkgPathArg string) error {
	if pkgPathArg == "" {
		return errors.New("specify a package")
	}
	switch *target {
	case "ios", "tvos", "android", "js", "windows", "macos":
	default:
		return fmt.Errorf("invalid target %q", *target)
	}
	switch *buildMode {
	case "archive", "exe":
	default:
		return fmt.Errorf("invalid build mode %q", *buildMode)
	}
	return nil
}

func build(bi *buildInfo) error {
	tmpDir, err := os.MkdirTemp("", "gogio-")
	if err != nil {
		return err
	}
	if *keepWorkdir {
		fmt.Fprintf(os.Stderr, "WORKDIR=%s\n", tmpDir)
	} else {
		defer os.RemoveAll(tmpDir)
	}
	switch *target {
	case "js":
		return buildJS(bi)
	case "ios", "tvos":
		return buildIOS(tmpDir, *target, bi)
	case "android":
		return buildAndroid(tmpDir, bi)
	case "windows":
		return buildWindows(tmpDir, bi)
	case "macos":
		return buildMac(tmpDir, bi)
	default:
		panic("unreachable")
	}
}

func runCmdRaw(cmd *exec.Cmd) ([]byte, error) {
	line := commandLine(cmd.Args)
	if *printCommands {
		fmt.Printf("%s\n", line)
	}
	done := progress.Command(cmd)
	out, err := cmd.Output()
	done(err)
	if err == nil {
		return out, nil
	}
	if err, ok := err.(*exec.ExitError); ok {
		return nil, fmt.Errorf("%s failed: %s%s", line, out, err.Stderr)
	}
	return nil, err
}

// commandLine joins a command's arguments for messages, hiding the value
// after --password (notarytool's).
func commandLine(args []string) string {
	shown := append([]string(nil), args...)
	for i := 1; i < len(shown); i++ {
		if shown[i-1] == "--password" {
			shown[i] = "***"
		}
	}
	return strings.Join(shown, " ")
}

func runCmd(cmd *exec.Cmd) (string, error) {
	out, err := runCmdRaw(cmd)
	return string(bytes.TrimSpace(out)), err
}

func copyFile(dst, src string) (err error) {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := w.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(w, r)
	return err
}

type arch struct {
	iosArch   string
	jniArch   string
	clangArch string
}

var allArchs = map[string]arch{
	"arm": {
		iosArch:   "armv7",
		jniArch:   "armeabi-v7a",
		clangArch: "armv7a-linux-androideabi",
	},
	"arm64": {
		iosArch:   "arm64",
		jniArch:   "arm64-v8a",
		clangArch: "aarch64-linux-android",
	},
	"386": {
		iosArch:   "i386",
		jniArch:   "x86",
		clangArch: "i686-linux-android",
	},
	"amd64": {
		iosArch:   "x86_64",
		jniArch:   "x86_64",
		clangArch: "x86_64-linux-android",
	},
}

type iconVariant struct {
	path string
	size int
	fill bool
}

func buildIcons(baseDir, icon string, variants []iconVariant) error {
	f, err := os.Open(icon)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	var resizes errgroup.Group
	for _, v := range variants {
		v := v
		resizes.Go(func() (err error) {
			path := filepath.Join(baseDir, v.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			defer func() {
				if cerr := f.Close(); err == nil {
					err = cerr
				}
			}()
			return png.Encode(f, resizeIcon(v, img))
		})
	}
	return resizes.Wait()
}

func resizeIcon(v iconVariant, img image.Image) *image.NRGBA {
	scaled := image.NewNRGBA(image.Rectangle{Max: image.Point{X: v.size, Y: v.size}})
	op := draw.Src
	if v.fill {
		op = draw.Over
		draw.Draw(scaled, scaled.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	}
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), img, img.Bounds(), op, nil)

	return scaled
}
