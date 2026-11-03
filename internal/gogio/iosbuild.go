// SPDX-License-Identifier: Unlicense OR MIT
//
// Derived from gioui.org/cmd/gogio (v0.10.3); see LICENSE in this directory.

package gogio

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	minIOSVersion = 10
	// Some Metal features require tvOS 11
	minTVOSVersion = 11
	// Metal is available from iOS 8 on devices, yet from version 13 on the
	// simulator.
	minSimulatorVersion = 13
)

func buildIOS(tmpDir, target string, bi *buildInfo) error {
	appName := bi.name
	switch *buildMode {
	case "archive":
		framework := *destPath
		if framework == "" {
			framework = fmt.Sprintf("%s.framework", UppercaseName(appName))
		}
		return archiveIOS(tmpDir, target, framework, bi)
	case "exe":
		out := *destPath
		if out == "" {
			out = appName + ".ipa"
		}
		forDevice := strings.HasSuffix(out, ".ipa")
		// Filter out unsupported architectures.
		for i := len(bi.archs) - 1; i >= 0; i-- {
			switch bi.archs[i] {
			case "arm", "arm64":
				if forDevice {
					continue
				}
			case "386", "amd64":
				if !forDevice {
					continue
				}
			}

			bi.archs = slices.Delete(bi.archs, i, i+1)
		}
		if !forDevice && !strings.HasSuffix(out, ".app") {
			return fmt.Errorf("the specified output directory %q does not end in .app or .ipa", out)
		}
		if !forDevice {
			return exeIOS(tmpDir, target, out, bi)
		}
		payload := filepath.Join(tmpDir, "Payload")
		appDir := filepath.Join(payload, appName+".app")
		if err := os.MkdirAll(appDir, 0o755); err != nil {
			return err
		}
		if err := exeIOS(tmpDir, target, appDir, bi); err != nil {
			return err
		}

		embedded := filepath.Join(appDir, "embedded.mobileprovision")

		var provisions []string
		if bi.key != "" {
			if ext := filepath.Ext(bi.key); ext != ".mobileprovision" && ext != ".provisionprofile" {
				return fmt.Errorf("sign: -signkey specifies an Apple provisioning profile, but %q does not end in .mobileprovision or .provisionprofile", bi.key)
			}
			provisions = []string{bi.key}
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}

			p, err := filepath.Glob(filepath.Join(home, "Library", "MobileDevice", "Provisioning Profiles", "*.mobileprovision"))
			if err != nil {
				return err
			}
			provisions = p
		}

		deep := true
		if ext := extras.ShareExtension; ext != nil {
			if ext.Profile == "" {
				return errors.New("sign: the share extension needs its own provisioning profile (ios.share_profile)")
			}
			appex := filepath.Join(appDir, "PlugIns", shareExtName+".appex")
			if err := signApple(shareExtID(bi.appID), tmpDir, filepath.Join(appex, "embedded.mobileprovision"), appex, []string{ext.Profile}, shareExtEntitlements(ext), false); err != nil {
				return fmt.Errorf("share extension: %w", err)
			}
			// --deep would re-sign the extension with the app's
			// entitlements.
			deep = false
		}
		if err := signApple(bi.appID, tmpDir, embedded, appDir, provisions, extras.Entitlements, deep); err != nil {
			return err
		}
		return zipDir(out, tmpDir, "Payload")
	default:
		panic("unreachable")
	}
}

// signApple is shared between iOS and macOS.
func signApple(appID, tmpDir, embedded, app string, provisions []string, ents map[string]any, deep bool) error {
	provInfo := filepath.Join(tmpDir, "provision.plist")
	var avail []string
	for _, prov := range provisions {
		// Decode the provision file to a plist.
		_, err := runCmd(exec.Command("security", "cms", "-D", "-i", prov, "-o", provInfo))
		if err != nil {
			return err
		}
		expUnix, err := runCmd(exec.Command("/usr/libexec/PlistBuddy", "-c", "Print:ExpirationDate", provInfo))
		if err != nil {
			return err
		}
		exp, err := time.Parse(time.UnixDate, expUnix)
		if err != nil {
			return fmt.Errorf("sign: failed to parse expiration date from %q: %v", prov, err)
		}
		if exp.Before(time.Now()) {
			continue
		}
		appIDPrefix, err := runCmd(exec.Command("/usr/libexec/PlistBuddy", "-c", "Print:ApplicationIdentifierPrefix:0", provInfo))
		if err != nil {
			return err
		}

		// iOS/macOS Catalyst
		provAppIDSearchKey := "Print:Entitlements:application-identifier"
		if filepath.Ext(prov) == ".provisionprofile" {
			// macOS
			provAppIDSearchKey = "Print:Entitlements:com.apple.application-identifier"
		}
		provAppID, err := runCmd(exec.Command("/usr/libexec/PlistBuddy", "-c", provAppIDSearchKey, provInfo))
		if err != nil {
			return err
		}
		expAppID := fmt.Sprintf("%s.%s", appIDPrefix, appID)
		avail = append(avail, provAppID)
		if expAppID != provAppID {
			continue
		}
		// Copy provisioning file.
		if err := copyFile(embedded, prov); err != nil {
			return err
		}
		certDER, err := runCmdRaw(exec.Command("/usr/libexec/PlistBuddy", "-c", "Print:DeveloperCertificates:0", provInfo))
		if err != nil {
			return err
		}
		// Omit trailing newline.
		certDER = certDER[:len(certDER)-1]
		entitlements, err := runCmd(exec.Command("/usr/libexec/PlistBuddy", "-x", "-c", "Print:Entitlements", provInfo))
		if err != nil {
			return err
		}
		entFile := filepath.Join(tmpDir, "entitlements.plist")
		if err := os.WriteFile(entFile, []byte(entitlements), 0o660); err != nil {
			return err
		}
		if err := applyEntitlements(entFile, ents); err != nil {
			return err
		}
		identity := sha1.Sum(certDER)
		idHex := hex.EncodeToString(identity[:])
		args := []string{"--sign", idHex}
		if deep {
			args = append(args, "--deep")
		}
		args = append(args, "--force", "--options", "runtime", "--entitlements", entFile, app)
		_, err = runCmd(exec.Command("codesign", args...))
		return err
	}
	return fmt.Errorf("sign: no valid provisioning profile found for bundle id %q among %v", appID, avail)
}

func exeIOS(tmpDir, target, app string, bi *buildInfo) error {
	if bi.appID == "" {
		return errors.New("app id is empty; use -appid to set it")
	}
	if err := os.RemoveAll(app); err != nil {
		return err
	}
	if err := os.Mkdir(app, 0o755); err != nil {
		return err
	}
	appName := UppercaseName(bi.name)
	exe := filepath.Join(app, appName)
	lipo := exec.Command("xcrun", "lipo", "-o", exe, "-create")
	var builds errgroup.Group
	for _, a := range bi.archs {
		clang, cflags, err := iosCompilerFor(target, a, bi.minsdk)
		if err != nil {
			return err
		}
		cflags = append(cflags,
			"-fobjc-arc",
			fmt.Sprintf("-miphoneos-version-min=%d.0", bi.minsdk),
		)
		cflagsLine := strings.Join(cflags, " ")
		exeSlice := filepath.Join(tmpDir, "app-"+a)
		lipo.Args = append(lipo.Args, exeSlice)
		compile := goCommand("build",
			"-ldflags=-s -w "+bi.ldflags,
			"-o", exeSlice,
			"-tags", bi.tags,
			bi.pkgPath,
		)
		compile.Env = append(
			os.Environ(),
			"GOOS=ios",
			"GOARCH="+a,
			"CGO_ENABLED=1",
			"CC="+clang,
			"CXX="+clang+"++",
			"CGO_CFLAGS="+cflagsLine,
			"CGO_CXXFLAGS="+cflagsLine,
			"CGO_LDFLAGS=-lresolv "+cflagsLine,
			iosDeploymentTarget(bi.minsdk),
		)
		builds.Go(func() error {
			_, err := runCmd(compile)
			return err
		})
	}
	if err := builds.Wait(); err != nil {
		return err
	}
	if _, err := runCmd(lipo); err != nil {
		return err
	}
	if len(extras.PrivacyManifest) > 0 {
		if err := os.WriteFile(filepath.Join(app, "PrivacyInfo.xcprivacy"), extras.PrivacyManifest, 0o644); err != nil {
			return err
		}
	}
	infoPlist := buildInfoPlist(bi)
	plistFile := filepath.Join(app, "Info.plist")
	if err := os.WriteFile(plistFile, []byte(infoPlist), 0o660); err != nil {
		return err
	}
	_, iconErr := os.Stat(bi.iconPath)
	if iconErr == nil || extras.SplashColor != "" {
		icon := ""
		if iconErr == nil {
			icon = bi.iconPath
		}
		assetPlist, err := iosIcons(bi, tmpDir, app, icon)
		if err != nil {
			return err
		}
		// Merge assets plist with Info.plist
		cmd := exec.Command(
			"/usr/libexec/PlistBuddy",
			"-c", "Merge "+assetPlist,
			plistFile,
		)
		if _, err := runCmd(cmd); err != nil {
			return err
		}
	}
	if _, err := runCmd(exec.Command("plutil", "-convert", "binary1", plistFile)); err != nil {
		return err
	}
	if ext := extras.ShareExtension; ext != nil {
		appex, err := buildShareExtension(tmpDir, target, app, bi, ext)
		if err != nil {
			return err
		}
		if forSimulator(bi.archs) {
			// Device builds are signed with their profiles later.
			if err := adhocSign(tmpDir, appex, shareExtEntitlements(ext)); err != nil {
				return err
			}
			return adhocSign(tmpDir, app, extras.Entitlements)
		}
	}
	return nil
}

// forSimulator reports whether archs are a simulator build's.
func forSimulator(archs []string) bool {
	for _, a := range archs {
		if a == "arm" || a == "arm64" {
			return false
		}
	}
	return true
}

// iosIcons builds an asset catalog - the app icon when icon isn't
// empty, the launch screen's colour with a splash colour - and compiles
// it with the Xcode command actool. It returns the asset plist file to
// be merged into Info.plist.
func iosIcons(bi *buildInfo, tmpDir, appDir, icon string) (string, error) {
	assets := filepath.Join(tmpDir, "Assets.xcassets")
	if err := os.Mkdir(assets, 0o700); err != nil {
		return "", err
	}
	if extras.SplashColor != "" {
		set := filepath.Join(assets, launchColorName+".colorset")
		if err := os.Mkdir(set, 0o700); err != nil {
			return "", err
		}
		js, err := colorSetJSON(extras.SplashColor, extras.SplashColorLight)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(set, "Contents.json"), js, 0o600); err != nil {
			return "", err
		}
	}
	args := []string{"--compile", appDir, "--platform", iosPlatformFor(bi.target)}
	if icon == "" {
		return compileAssets(bi, tmpDir, assets, args)
	}
	appIcon := filepath.Join(assets, "AppIcon.appiconset")
	err := buildIcons(appIcon, icon, []iconVariant{
		{path: "ios_2x.png", size: 120},
		{path: "ios_3x.png", size: 180},
		{path: "ipad_1x.png", size: 76},
		{path: "ipad_2x.png", size: 152},
		{path: "ipad_4x.png", size: 228},
		// The App Store icon is not allowed to contain
		// transparent pixels.
		{path: "ios_store.png", size: 1024, fill: true},
	})
	if err != nil {
		return "", err
	}
	contentJson := `{
"images": [
    {
        "size": "60x60",
        "idiom": "iphone",
        "filename": "ios_2x.png",
        "scale": "2x"
    },
    {
        "size": "60x60",
        "idiom": "iphone",
        "filename": "ios_3x.png",
        "scale": "3x"
    },
    {
        "size": "76x76",
        "idiom": "ipad",
        "filename": "ipad_1x.png",
        "scale": "1x"
    },
    {
        "size": "76x76",
        "idiom": "ipad",
        "filename": "ipad_2x.png",
        "scale": "2x"
    },
    {
        "size": "152x152",
        "idiom": "ipad",
        "filename": "ipad_4x.png",
        "scale": "2x"
    },
    {
        "size": "1024x1024",
        "idiom": "ios-marketing",
        "filename": "ios_store.png",
        "scale": "1x"
    }
]
}`
	contentFile := filepath.Join(appIcon, "Contents.json")
	if err := os.WriteFile(contentFile, []byte(contentJson), 0o600); err != nil {
		return "", err
	}
	return compileAssets(bi, tmpDir, assets, append(args, "--app-icon", "AppIcon"))
}

// compileAssets runs actool on an asset catalog.
func compileAssets(bi *buildInfo, tmpDir, assets string, args []string) (string, error) {
	assetPlist := filepath.Join(tmpDir, "assets.plist")
	minsdk := bi.minsdk
	if minsdk == 0 {
		minsdk = minIOSVersion
	}
	args = append(args,
		"--minimum-deployment-target", strconv.Itoa(minsdk),
		"--output-partial-info-plist", assetPlist,
		assets)
	_, err := runCmd(exec.Command("actool", args...))
	return assetPlist, err
}

// launchColorName names the launch screen's colour in the asset
// catalog.
const launchColorName = "GiodeLaunchBackground"

// colorSetJSON is an asset catalog colour set: dark ("#RRGGBB") for
// dark mode and the default, light for light mode when set.
func colorSetJSON(dark, light string) ([]byte, error) {
	comp := func(hex string) (map[string]any, error) {
		h := strings.TrimPrefix(hex, "#")
		if len(h) != 6 {
			return nil, fmt.Errorf("splash colour %q isn't #RRGGBB", hex)
		}
		if _, err := strconv.ParseUint(h, 16, 32); err != nil {
			return nil, fmt.Errorf("splash colour %q isn't #RRGGBB", hex)
		}
		return map[string]any{
			"color-space": "srgb",
			"components": map[string]string{
				"red": "0x" + h[0:2], "green": "0x" + h[2:4], "blue": "0x" + h[4:6], "alpha": "1.000",
			},
		}, nil
	}
	d, err := comp(dark)
	if err != nil {
		return nil, err
	}
	def := d
	colors := []map[string]any{}
	if light != "" {
		l, err := comp(light)
		if err != nil {
			return nil, err
		}
		def = l
	}
	colors = append(colors, map[string]any{"idiom": "universal", "color": def})
	colors = append(colors, map[string]any{
		"idiom":       "universal",
		"appearances": []map[string]string{{"appearance": "luminosity", "value": "dark"}},
		"color":       d,
	})
	return json.MarshalIndent(map[string]any{
		"colors": colors,
		"info":   map[string]any{"author": "giode", "version": 1},
	}, "", "  ")
}

func buildInfoPlist(bi *buildInfo) string {
	appName := UppercaseName(bi.name)
	platform := iosPlatformFor(bi.target)
	var supportPlatform string
	switch bi.target {
	case "ios":
		supportPlatform = "iPhoneOS"
	case "tvos":
		supportPlatform = "AppleTVOS"
	}

	manifestSrc := struct {
		AppName         string
		AppID           string
		Version         string
		VersionCode     uint32
		Platform        string
		MinVersion      int
		SupportPlatform string
		Schemes         []string
		// SplashColor names the launch screen's colour set, when there's
		// a splash colour.
		SplashColor string
	}{
		AppName:         appName,
		AppID:           bi.appID,
		Version:         bi.version.StringCompact(),
		VersionCode:     bi.version.VersionCode,
		Platform:        platform,
		MinVersion:      bi.minsdk,
		SupportPlatform: supportPlatform,
		Schemes:         bi.schemes,
	}
	if extras.SplashColor != "" {
		manifestSrc.SplashColor = launchColorName
	}

	tmpl, err := template.New("manifest").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleExecutable</key>
	<string>{{.AppName}}</string>
	<key>CFBundleIdentifier</key>
	<string>{{.AppID}}</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>{{.AppName}}</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>{{.Version}}</string>
	<key>CFBundleVersion</key>
	<string>{{.VersionCode}}</string>
	{{- if not .SplashColor}}
	<key>UILaunchStoryboardName</key>
	<string>LaunchScreen</string>
	{{- end}}
	<key>UIRequiredDeviceCapabilities</key>
	<array><string>arm64</string></array>
	<key>DTPlatformName</key>
	<string>{{.Platform}}</string>
	<key>DTPlatformVersion</key>
	<string>12.4</string>
	<key>MinimumOSVersion</key>
	<string>{{.MinVersion}}.0</string>
	<key>UIDeviceFamily</key>
	<array>
		<integer>1</integer>
		<integer>2</integer>
	</array>
	<key>CFBundleSupportedPlatforms</key>
	<array>
		<string>{{.SupportPlatform}}</string>
	</array>
	<key>UISupportedInterfaceOrientations</key>
	<array>
		<string>UIInterfaceOrientationPortrait</string>
		<string>UIInterfaceOrientationPortraitUpsideDown</string>
		<string>UIInterfaceOrientationLandscapeLeft</string>
		<string>UIInterfaceOrientationLandscapeRight</string>
	</array>
	<key>UIRequiresFullScreen</key>
	<true/>
	<key>DTCompiler</key>
	<string>com.apple.compilers.llvm.clang.1_0</string>
	<key>DTPlatformBuild</key>
	<string>16G73</string>
	<key>DTSDKBuild</key>
	<string>16G73</string>
	<key>DTSDKName</key>
	<string>{{.Platform}}12.4</string>
	<key>DTXcode</key>
	<string>1030</string>
	<key>DTXcodeBuild</key>
	<string>10G8</string>
	<key>UILaunchScreen</key>
	{{- if .SplashColor}}
	<dict>
		<key>UIColorName</key>
		<string>{{.SplashColor}}</string>
	</dict>
	{{- else}}
	<true/>
	{{- end}}
    {{if .Schemes}}
	<key>CFBundleURLTypes</key>
	<array>
	  {{range .Schemes}}
	  <dict>
		<key>CFBundleURLSchemes</key>
		<array>
		  <string>{{.}}</string>
		</array>
	  </dict>
	  {{end}}
	</array>
    {{end}}
</dict>
</plist>`)
	if err != nil {
		panic(err)
	}

	var manifestBuffer bytes.Buffer
	if err := tmpl.Execute(&manifestBuffer, manifestSrc); err != nil {
		panic(err)
	}

	return withPlistExtras(manifestBuffer.String())
}

// withPlistExtras adds Extras.InfoPlist to the end of a plist's
// top-level dictionary.
func withPlistExtras(plist string) string {
	if extras.InfoPlist == "" {
		return plist
	}
	i := strings.LastIndex(plist, "</dict>")
	if i < 0 {
		return plist
	}
	return plist[:i] + extras.InfoPlist + "\n" + plist[i:]
}

func iosPlatformFor(target string) string {
	switch target {
	case "ios":
		return "iphoneos"
	case "tvos":
		return "appletvos"
	default:
		panic("invalid platform " + target)
	}
}

func archiveIOS(tmpDir, target, frameworkRoot string, bi *buildInfo) error {
	framework := filepath.Base(frameworkRoot)
	const suf = ".framework"
	if !strings.HasSuffix(framework, suf) {
		return fmt.Errorf("the specified output %q does not end in '.framework'", frameworkRoot)
	}
	framework = framework[:len(framework)-len(suf)]
	if err := os.RemoveAll(frameworkRoot); err != nil {
		return err
	}
	frameworkDir := filepath.Join(frameworkRoot, "Versions", "A")
	for _, dir := range []string{"Headers", "Modules"} {
		p := filepath.Join(frameworkDir, dir)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	symlinks := [][2]string{
		{"Versions/Current/Headers", "Headers"},
		{"Versions/Current/Modules", "Modules"},
		{"Versions/Current/" + framework, framework},
		{"A", filepath.Join("Versions", "Current")},
	}
	for _, l := range symlinks {
		if err := os.Symlink(l[0], filepath.Join(frameworkRoot, l[1])); err != nil && !os.IsExist(err) {
			return err
		}
	}
	exe := filepath.Join(frameworkDir, framework)
	lipo := exec.Command("xcrun", "lipo", "-o", exe, "-create")
	var builds errgroup.Group
	tags := bi.tags
	for _, a := range bi.archs {
		clang, cflags, err := iosCompilerFor(target, a, bi.minsdk)
		if err != nil {
			return err
		}
		lib := filepath.Join(tmpDir, "gio-"+a)
		cmd := goCommand("build",
			"-ldflags=-s -w "+bi.ldflags,
			"-buildmode=c-archive",
			"-o", lib,
			"-tags", tags,
			bi.pkgPath,
		)
		lipo.Args = append(lipo.Args, lib)
		cflagsLine := strings.Join(cflags, " ")
		cmd.Env = append(
			os.Environ(),
			"GOOS=ios",
			"GOARCH="+a,
			"CGO_ENABLED=1",
			"CC="+clang,
			"CGO_CFLAGS="+cflagsLine,
			"CGO_LDFLAGS="+cflagsLine,
			iosDeploymentTarget(bi.minsdk),
		)
		builds.Go(func() error {
			_, err := runCmd(cmd)
			return err
		})
	}
	if err := builds.Wait(); err != nil {
		return err
	}
	if _, err := runCmd(lipo); err != nil {
		return err
	}
	appDir, err := runCmd(goCommand("list", "-tags", tags, "-f", "{{.Dir}}", "gioui.org/app/"))
	if err != nil {
		return err
	}
	headerDst := filepath.Join(frameworkDir, "Headers", framework+".h")
	headerSrc := filepath.Join(appDir, "framework_ios.h")
	if err := copyFile(headerDst, headerSrc); err != nil {
		return err
	}
	module := fmt.Sprintf(`framework module "%s" {
    header "%[1]s.h"

    export *
}`, framework)
	moduleFile := filepath.Join(frameworkDir, "Modules", "module.modulemap")
	return os.WriteFile(moduleFile, []byte(module), 0o644)
}

func iosCompilerFor(target, arch string, minsdk int) (string, []string, error) {
	var (
		platformSDK string
		platformOS  string
	)
	switch target {
	case "ios":
		platformOS = "ios"
		platformSDK = "iphone"
	case "tvos":
		platformOS = "tvos"
		platformSDK = "appletv"
	}
	switch arch {
	case "arm", "arm64":
		platformSDK += "os"
		if minsdk == 0 {
			minsdk = minIOSVersion
			if target == "tvos" {
				minsdk = minTVOSVersion
			}
		}
	case "386", "amd64":
		platformOS += "-simulator"
		platformSDK += "simulator"
		if minsdk == 0 {
			minsdk = minSimulatorVersion
		}
	default:
		return "", nil, fmt.Errorf("unsupported -arch: %s", arch)
	}
	sdkPath, err := runCmd(exec.Command("xcrun", "--sdk", platformSDK, "--show-sdk-path"))
	if err != nil {
		return "", nil, err
	}
	clang, err := runCmd(exec.Command("xcrun", "--sdk", platformSDK, "--find", "clang"))
	if err != nil {
		return "", nil, err
	}
	cflags := []string{
		"-arch", allArchs[arch].iosArch,
		"-isysroot", sdkPath,
		"-m" + platformOS + "-version-min=" + strconv.Itoa(minsdk),
	}
	return clang, cflags, nil
}

func zipDir(dst, base, dir string) (err error) {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	zipf := zip.NewWriter(f)
	err = filepath.Walk(filepath.Join(base, dir), func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if f.IsDir() {
			return nil
		}
		rel := filepath.ToSlash(path[len(base)+1:])
		entry, err := zipf.Create(rel)
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(entry, src)
		return err
	})
	if err != nil {
		return err
	}
	return zipf.Close()
}

// applyEntitlements replaces or adds entitlements in the plist file:
// giode's (associated domains, push) on top of the profile's.
func applyEntitlements(file string, ents map[string]any) error {
	keys := make([]string, 0, len(ents))
	for k := range ents {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args := []string{"-c", "Delete :" + k}
		switch v := ents[k].(type) {
		case string:
			args = append(args, "-c", "Add :"+k+" string "+v)
		case bool:
			args = append(args, "-c", fmt.Sprintf("Add :%s bool %t", k, v))
		case []string:
			args = append(args, "-c", "Add :"+k+" array")
			for i, e := range v {
				args = append(args, "-c", fmt.Sprintf("Add :%s:%d string %s", k, i, e))
			}
		default:
			return fmt.Errorf("entitlement %s: unsupported value %T", k, v)
		}
		// Delete fails when the key is missing; PlistBuddy carries on
		// with the other commands.
		cmd := exec.Command("/usr/libexec/PlistBuddy", append(args, file)...)
		if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(out), "Does Not Exist") {
			return fmt.Errorf("entitlement %s: %v: %s", k, err, out)
		}
	}
	return nil
}

// iosDeploymentTarget is the IPHONEOS_DEPLOYMENT_TARGET for an iOS
// build's commands: Go's cgo build of the iOS runtime needs one, or
// clang gets -miphoneos-version-min=0.0. It's set for those commands
// only - clang reads it, so set in giode's own environment it would
// make a later macOS build in the same run target the iPhone. One
// already in the environment wins.
func iosDeploymentTarget(minsdk int) string {
	if v := os.Getenv("IPHONEOS_DEPLOYMENT_TARGET"); v != "" {
		return "IPHONEOS_DEPLOYMENT_TARGET=" + v
	}
	return fmt.Sprintf("IPHONEOS_DEPLOYMENT_TARGET=%d.0", minsdk)
}
