package gogio

import (
	_ "embed"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"
)

// ShareExtension builds iOS's share extension into the app (giode.toml's
// app.share_target).
type ShareExtension struct {
	// AppGroup is the App Group the extension and the app share.
	AppGroup string
	// Scheme is the URL scheme the extension opens the app with.
	Scheme string
	// Profile is the provisioning profile signing the extension (bundle
	// ID <app id>.share) in device builds.
	Profile string
}

//go:embed shareext/GiodeShare.m
var shareExtSource []byte

const shareExtName = "Share"

// shareExtID is the extension's bundle ID.
func shareExtID(appID string) string { return appID + ".share" }

// buildShareExtension compiles the share extension into
// app/PlugIns/Share.appex and returns its path.
func buildShareExtension(tmpDir, target, app string, bi *buildInfo, ext *ShareExtension) (string, error) {
	appex := filepath.Join(app, "PlugIns", shareExtName+".appex")
	if err := os.MkdirAll(appex, 0o755); err != nil {
		return "", err
	}
	src := filepath.Join(tmpDir, "GiodeShare.m")
	if err := os.WriteFile(src, shareExtSource, 0o644); err != nil {
		return "", err
	}
	exe := filepath.Join(appex, shareExtName)
	lipo := exec.Command("xcrun", "lipo", "-o", exe, "-create")
	var builds errgroup.Group
	for _, a := range bi.archs {
		clang, cflags, err := iosCompilerFor(target, a, bi.minsdk)
		if err != nil {
			return "", err
		}
		slice := filepath.Join(tmpDir, "share-"+a)
		lipo.Args = append(lipo.Args, slice)
		args := append(cflags,
			"-fobjc-arc", "-fmodules", "-fapplication-extension",
			"-e", "_NSExtensionMain",
			"-framework", "Foundation", "-framework", "UIKit", "-framework", "MobileCoreServices",
			"-o", slice, src,
		)
		cmd := exec.Command(clang, args...)
		builds.Go(func() error {
			_, err := runCmd(cmd)
			return err
		})
	}
	if err := builds.Wait(); err != nil {
		return "", fmt.Errorf("share extension: %w", err)
	}
	if _, err := runCmd(lipo); err != nil {
		return "", err
	}
	plist := filepath.Join(appex, "Info.plist")
	if err := os.WriteFile(plist, []byte(shareExtPlist(bi, ext)), 0o644); err != nil {
		return "", err
	}
	if _, err := runCmd(exec.Command("plutil", "-convert", "binary1", plist)); err != nil {
		return "", err
	}
	return appex, nil
}

// shareExtPlist is the share extension's Info.plist: a share-services
// extension taking files, images, videos, text and links.
func shareExtPlist(bi *buildInfo, ext *ShareExtension) string {
	platform := iosPlatformFor(bi.target)
	esc := html.EscapeString
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleDisplayName</key>
	<string>` + esc(bi.name) + `</string>
	<key>CFBundleExecutable</key>
	<string>` + shareExtName + `</string>
	<key>CFBundleIdentifier</key>
	<string>` + esc(shareExtID(bi.appID)) + `</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>` + shareExtName + `</string>
	<key>CFBundlePackageType</key>
	<string>XPC!</string>
	<key>CFBundleShortVersionString</key>
	<string>` + bi.version.StringCompact() + `</string>
	<key>CFBundleVersion</key>
	<string>` + fmt.Sprint(bi.version.VersionCode) + `</string>
	<key>CFBundleSupportedPlatforms</key>
	<array>
		<string>iPhoneOS</string>
	</array>
	<key>DTPlatformName</key>
	<string>` + platform + `</string>
	<key>MinimumOSVersion</key>
	<string>` + fmt.Sprint(bi.minsdk) + `.0</string>
	<key>UIRequiredDeviceCapabilities</key>
	<array><string>arm64</string></array>
	<key>UIDeviceFamily</key>
	<array>
		<integer>1</integer>
		<integer>2</integer>
	</array>
	<key>GiodeAppGroup</key>
	<string>` + esc(ext.AppGroup) + `</string>
	<key>GiodeShareScheme</key>
	<string>` + esc(ext.Scheme) + `</string>
	<key>NSExtension</key>
	<dict>
		<key>NSExtensionPointIdentifier</key>
		<string>com.apple.share-services</string>
		<key>NSExtensionPrincipalClass</key>
		<string>GiodeShareViewController</string>
		<key>NSExtensionAttributes</key>
		<dict>
			<key>NSExtensionActivationRule</key>
			<dict>
				<key>NSExtensionActivationSupportsAttachmentsWithMaxCount</key>
				<integer>100</integer>
				<key>NSExtensionActivationSupportsFileWithMaxCount</key>
				<integer>100</integer>
				<key>NSExtensionActivationSupportsImageWithMaxCount</key>
				<integer>100</integer>
				<key>NSExtensionActivationSupportsMovieWithMaxCount</key>
				<integer>100</integer>
				<key>NSExtensionActivationSupportsText</key>
				<true/>
				<key>NSExtensionActivationSupportsWebURLWithMaxCount</key>
				<integer>1</integer>
			</dict>
		</dict>
	</dict>
</dict>
</plist>
`
}

// shareExtEntitlements are the extension's: the App Group.
func shareExtEntitlements(ext *ShareExtension) map[string]any {
	return map[string]any{"com.apple.security.application-groups": []string{ext.AppGroup}}
}

// adhocSign signs a simulator bundle without an identity, with
// entitlements: the simulator reads the App Group from them.
func adhocSign(tmpDir, bundle string, ents map[string]any) error {
	f := filepath.Join(tmpDir, "adhoc-"+filepath.Base(bundle)+".entitlements")
	if err := os.WriteFile(f, []byte(entitlementsXML(ents)), 0o644); err != nil {
		return err
	}
	_, err := runCmd(exec.Command("codesign", "--sign", "-", "--force", "--timestamp=none", "--entitlements", f, bundle))
	return err
}

// entitlementsXML renders entitlements (strings, booleans, lists of
// strings) as a plist.
func entitlementsXML(ents map[string]any) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	for _, k := range sortedKeys(ents) {
		fmt.Fprintf(&b, "\t<key>%s</key>\n", html.EscapeString(k))
		switch v := ents[k].(type) {
		case string:
			fmt.Fprintf(&b, "\t<string>%s</string>\n", html.EscapeString(v))
		case bool:
			if v {
				b.WriteString("\t<true/>\n")
			} else {
				b.WriteString("\t<false/>\n")
			}
		case []string:
			b.WriteString("\t<array>\n")
			for _, e := range v {
				fmt.Fprintf(&b, "\t\t<string>%s</string>\n", html.EscapeString(e))
			}
			b.WriteString("\t</array>\n")
		}
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}
