// Package config reads giode.toml, the project file of a giode app, and
// turns it into build settings: the app's identity, and the Android
// manifest and Info.plist entries gogio can't add on its own.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileName is the project file giode looks for.
const FileName = "giode.toml"

// Config is the content of giode.toml.
type Config struct {
	App     App     `toml:"app"`
	Android Android `toml:"android"`
	IOS     IOS     `toml:"ios"`
	MacOS   MacOS   `toml:"macos"`
	Windows Windows `toml:"windows"`
	Linux   Linux   `toml:"linux"`
	Web     Web     `toml:"web"`

	// Update configures self-updates of desktop builds.
	Update Update `toml:"update"`

	// Privacy is the privacy manifest for iOS and macOS.
	Privacy Privacy `toml:"privacy"`

	// ContextActions add entries to the file manager's right-click menu
	// that open the app with the chosen paths.
	ContextActions []ContextAction `toml:"context_actions"`
	// Native bundles the shared libraries a cgo app loads.
	Native Native `toml:"native"`
	// FileTypes are the file types the app opens: "Open with" on every
	// desktop.
	FileTypes []FileType `toml:"file_types"`
	// CI configures giode ci.
	CI CI `toml:"ci"`

	// Dir is the directory holding giode.toml; paths in it are
	// relative to Dir.
	Dir string `toml:"-"`
}

// App is the app's identity, shared by every platform.
type App struct {
	// Name is the display name.
	Name string `toml:"name"`
	// ID is the reverse-DNS app ID (com.example.notes): the Android
	// package name and the Apple bundle ID.
	ID string `toml:"id"`
	// Version is major.minor.patch; Build is the build number (Android
	// versionCode, CFBundleVersion), at least 1, which stores require to
	// grow with every upload. A four-part version (1.4.2.7) sets both:
	// its fourth part is the build.
	Version string `toml:"version"`
	Build   int    `toml:"build"`
	// Icon is a square PNG, at least 512x512 for the stores.
	Icon string `toml:"icon"`
	// SplashColor ("#RRGGBB") fills the screen on Android and iOS until
	// the first frame: the window background and Android 12+'s splash
	// screen (with the app's icon), and iOS's launch screen.
	// SplashColorLight, when set, is used in light mode instead; then
	// SplashColor is the dark mode one. Empty keeps white.
	SplashColor      string `toml:"splash_color"`
	SplashColorLight string `toml:"splash_color_light"`
	// Package is the main Go package, "." by default.
	Package string `toml:"package"`
	// Schemes are URL schemes the app opens (myapp://...).
	Schemes []string `toml:"schemes"`
	// Links are https URLs the app opens instead of the browser -
	// universal links on iOS and macOS, App Links on Android - like
	// https://example.com/app/*. giode links writes the files the site
	// must serve to prove the app may.
	Links []string `toml:"links"`
	// ShareTarget offers the app in the system share sheet, for any
	// files and text, which reach giode.Config.OnShareReceived. On
	// Android it registers for ACTION_SEND and ACTION_SEND_MULTIPLE; on
	// iOS it builds, signs and embeds a share extension that hands the
	// items to the app through an App Group (ios.app_group).
	ShareTarget bool `toml:"share_target"`
	// Publisher is who makes the app, shown by installers and in
	// Windows' list of installed apps; Description a one-line summary;
	// Website the app's home page.
	Publisher   string `toml:"publisher"`
	Description string `toml:"description"`
	Website     string `toml:"website"`
	// Tags are Go build tags; Ldflags extra linker flags, in which
	// {version}, {build} and {id} become app.version (the release tag
	// with $GIODE_VERSION), app.build and app.id:
	// "-X main.version={version}". Every build also sets
	// giode.AppVersion and giode.AppBuild.
	Tags    string `toml:"tags"`
	Ldflags string `toml:"ldflags"`
	// Renderer "opengl" builds macOS and iOS apps with Gio's OpenGL
	// renderer instead of Metal (the nometal tag), for an app setting
	// giode.Config.Renderer to RendererOpenGL - an OpenGL video or 3D
	// engine drawing under the UI (Config.Underlay).
	Renderer string `toml:"renderer"`
	// GioPatches builds against a copy of Gio with giode's patches: the
	// hook behind Config.Underlay and Config.Renderer everywhere, and
	// android.gio_patches' fixes on Android. True by default.
	GioPatches *bool `toml:"gio_patches"`
	// Licenses is a JSON file every build refreshes with the licenses of
	// the app's modules (see giode licenses).
	Licenses string `toml:"licenses"`
	// Output is where builds go, "dist" by default.
	Output string `toml:"output"`
	// Platforms are the platforms the app is for: windows, macos,
	// linux, android, ios, web.
	Platforms []string `toml:"platforms"`
}

// Android configures Android builds.
type Android struct {
	MinSDK    int `toml:"min_sdk"`
	TargetSDK int `toml:"target_sdk"`
	// Arch limits the architectures built (arm, arm64, 386, amd64);
	// all four by default.
	Arch []string `toml:"arch"`
	// Permissions are added to the manifest: gogio's short names
	// (network, camera, microphone, storage, bluetooth, wakelock,
	// networkstate), giode's (notifications, foreground_service,
	// vibrate, biometric, location, coarse_location, contacts,
	// calendar, boot) or full names (android.permission.X).
	Permissions []string `toml:"permissions"`
	// GioPatches builds against a copy of Gio with giode's fixes for
	// Android: rendering at the display's full refresh rate (Gio as
	// released holds 120Hz displays at 60fps). True by default.
	GioPatches *bool `toml:"gio_patches"`
	// KeepAlive declares giode's foreground service, so App.KeepAlive
	// keeps work running after the app leaves the screen. It adds the
	// service and its permissions.
	KeepAlive bool `toml:"keep_alive"`
	// Notifications adds POST_NOTIFICATIONS, which App.Notify needs on
	// Android 13 and later. True by default.
	Notifications *bool `toml:"notifications"`
	// Queries are package names the app may look up (Android 11 package
	// visibility).
	Queries []string `toml:"queries"`
	// Manifest is raw XML added inside <manifest>; Application raw XML
	// added inside <application>.
	Manifest    string `toml:"manifest"`
	Application string `toml:"application"`
	// ApplicationAttributes are attributes set on <application>, by
	// name: requestLegacyExternalStorage = "true" (android: is added to
	// names without a namespace).
	ApplicationAttributes map[string]string `toml:"application_attributes"`
	// Keystore signs release builds (giode build -release);
	// KeystorePassEnv names the environment variable holding its
	// password, GIODE_KEYSTORE_PASS by default. Debug builds use the
	// Android debug key.
	Keystore        string `toml:"keystore"`
	KeystorePassEnv string `toml:"keystore_pass_env"`
	// Fingerprints are SHA-256 fingerprints of more signing
	// certificates allowed to open app.links: Play App Signing's, from
	// the Play Console. The keystore's is added by giode links.
	Fingerprints []string `toml:"fingerprints"`
	// Dependencies are Android libraries from Maven repositories
	// (group:artifact:version), built in with their own dependencies:
	// their classes, resources, manifest entries and native libraries.
	// giode packages that need one (push, billing, review) add theirs.
	Dependencies []string `toml:"dependencies"`
	// Repositories replace the Maven repositories searched (Google's
	// and Maven Central).
	Repositories []string `toml:"repositories"`
	// Libraries are local .aar and .jar files built in.
	Libraries []string `toml:"libraries"`
	// IconForeground, IconBackground and IconMonochrome are the
	// adaptive icon's layers (Android 8 and later): 108dp squares of
	// which launchers show the middle 72dp, cut to a circle, squircle or
	// other shape, with the artwork kept inside the middle 66dp.
	// IconForeground is a PNG over the whole canvas; without it, the app
	// icon is shrunk into the safe zone. IconBackground is a PNG or a
	// color ("#0f172a"); without it, the color around the app icon's
	// edge. IconMonochrome is a PNG whose shape Android 13 and later
	// tint for themed icons; without it, themed icons show the full
	// color icon.
	IconForeground string `toml:"icon_foreground"`
	IconBackground string `toml:"icon_background"`
	IconMonochrome string `toml:"icon_monochrome"`
	// Java are directories (searched recursively) or files of the
	// app's own Java sources, compiled with Gio's against the SDK's
	// android.jar (Java 8 language level) and built in: classes the
	// app calls through giode's android package.
	Java []string `toml:"java"`
	// GoogleServices is the google-services.json file from the Firebase
	// console, needed for push notifications (giode/push).
	GoogleServices string `toml:"google_services"`
}

// IOS configures iOS builds.
type IOS struct {
	MinOS int `toml:"min_os"`
	// Icon is the iOS app icon, a square PNG of 1024x1024 with no
	// transparency: iOS rounds the corners itself and shows transparent
	// pixels white. Without it iOS takes app.icon.
	Icon string `toml:"icon"`
	// Usage holds the texts iOS shows when asking for a permission, by
	// short name: camera, microphone, speech, photos, photos_add,
	// location, location_always, contacts, calendar, reminders,
	// bluetooth, faceid, motion, health, health_update, tracking,
	// local_network. iOS ends an app that uses one without its text.
	Usage map[string]string `toml:"usage"`
	// Plist adds raw Info.plist keys: strings, booleans, integers and
	// lists of strings.
	Plist map[string]any `toml:"plist"`
	// Profile is the provisioning profile signing device builds;
	// $GIODE_IOS_PROFILE overrides it.
	Profile string `toml:"profile"`
	// TeamID is the Apple developer team ID, for universal links'
	// apple-app-site-association file; $GIODE_IOS_TEAM_ID overrides it.
	TeamID string `toml:"team_id"`
	// Push enables push notifications (giode/push): "development" or
	// "production", the APNs environment of the aps-environment
	// entitlement, which the provisioning profile must allow. It also
	// lets silent pushes wake the app (UIBackgroundModes).
	Push string `toml:"push"`
	// AppGroup is the App Group app.share_target's extension shares with
	// the app, "group.<app.id>" by default. Both provisioning profiles
	// must allow it.
	AppGroup string `toml:"app_group"`
	// ShareProfile is the provisioning profile signing the share
	// extension (bundle ID <app.id>.share) in device builds with
	// app.share_target; $GIODE_IOS_SHARE_PROFILE overrides it.
	ShareProfile string `toml:"share_profile"`
}

// MacOS configures macOS app bundles.
type MacOS struct {
	// MinOS is the oldest macOS the app runs on, its Info.plist's
	// LSMinimumSystemVersion and the deployment target its cgo code is
	// built for; 11, Go's oldest, by default.
	MinOS int               `toml:"min_os"`
	Usage map[string]string `toml:"usage"`
	Plist map[string]any    `toml:"plist"`
	// Identity is the signing identity in the keychain ("Developer ID
	// Application: Name (TEAMID)") release builds are signed with, or
	// Profile a .provisionprofile for App Store builds. Without either,
	// release builds use the keychain's Developer ID Application
	// identity when there's one; $GIODE_MACOS_IDENTITY overrides it.
	Identity string `toml:"identity"`
	Profile  string `toml:"profile"`
	// Notarization, with the app-specific password read from
	// NotaryPassEnv (GIODE_NOTARY_PASS by default); $GIODE_NOTARY_ID and
	// $GIODE_NOTARY_TEAM_ID override the Apple ID and team, which is
	// otherwise the identity's.
	NotaryID      string `toml:"notary_id"`
	NotaryTeamID  string `toml:"notary_team_id"`
	NotaryPassEnv string `toml:"notary_pass_env"`
	// Push enables push notifications, as ios.push. It needs Profile:
	// macOS refuses the entitlement without a provisioning profile.
	Push string `toml:"push"`
	// Store configures Mac App Store builds (giode build mas).
	Store MacOSStore `toml:"store"`
}

// MacOSStore configures Mac App Store builds: an app signed with the
// Apple Distribution certificate through its provisioning profile, in
// the App Sandbox, wrapped in a signed installer package.
type MacOSStore struct {
	// Profile is the Mac App Store provisioning profile
	// (.provisionprofile); macos.profile by default, and
	// $GIODE_MACOS_STORE_PROFILE overrides it. Its certificate,
	// Apple Distribution, signs the app; it must be in the keychain.
	Profile string `toml:"profile"`
	// InstallerIdentity signs the .pkg: "3rd Party Mac Developer
	// Installer: Name (TEAMID)" (or "Mac Installer Distribution: ...").
	// By default, the first such identity in the keychain.
	InstallerIdentity string `toml:"installer_identity"`
	// Entitlements are the sandbox's: "network.client", "network.server",
	// "files.user-selected.read-write", "files.downloads.read-write", ...
	// (com.apple.security. is added to names without a dot-separated
	// prefix of their own; full names work too). The sandbox itself
	// (app-sandbox) is always on, and folders the user chooses keep
	// their bookmarks (files.bookmarks.app-scope) when user-selected
	// files are.
	Entitlements []string `toml:"entitlements"`
}

// StoreEntitlements returns the Mac App Store build's sandbox
// entitlements, all true.
func (c *Config) StoreEntitlements() map[string]any {
	ents := map[string]any{"com.apple.security.app-sandbox": true}
	userSelected := false
	for _, e := range c.MacOS.Store.Entitlements {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, "com.apple.") {
			e = "com.apple.security." + e
		}
		ents[e] = true
		if strings.HasPrefix(e, "com.apple.security.files.user-selected.") {
			userSelected = true
		}
	}
	if userSelected {
		ents["com.apple.security.files.bookmarks.app-scope"] = true
	}
	return ents
}

// Windows configures Windows builds.
type Windows struct {
	// Arch is the GOARCH, amd64 by default.
	Arch string `toml:"arch"`
	// CC turns on cgo for Windows builds, compiling C with it: a
	// MinGW-w64 compiler ("x86_64-w64-mingw32-gcc" cross-compiling,
	// "gcc" in MSYS2). Gio itself doesn't need cgo on Windows; an app
	// linking a C library (libmpv) does. CXX is the C++ compiler, and
	// PkgConfigPath the directory (or list) pkg-config searches for the
	// Windows libraries' .pc files; PkgConfig the pkg-config command
	// ("x86_64-w64-mingw32-pkg-config"), pkg-config by default.
	CC            string           `toml:"cc"`
	CXX           string           `toml:"cxx"`
	PkgConfig     string           `toml:"pkg_config"`
	PkgConfigPath string           `toml:"pkg_config_path"`
	Signing       WindowsSigning   `toml:"signing"`
	Installer     WindowsInstaller `toml:"installer"`
	MSIX          WindowsMSIX      `toml:"msix"`
	Winget        WindowsWinget    `toml:"winget"`
}

// WindowsMSIX configures MSIX packages (giode build msix), for the
// Microsoft Store and App Installer.
type WindowsMSIX struct {
	// Publisher is the signing certificate's subject ("CN=Example
	// Ltd"), or the publisher ID Partner Center gives for the Store.
	Publisher string `toml:"publisher"`
	// IdentityName is the package name; the Store reserves it. The app
	// ID by default.
	IdentityName string `toml:"identity_name"`
	// PublisherDisplayName is the publisher shown; app.publisher by
	// default.
	PublisherDisplayName string `toml:"publisher_display_name"`
	// StartupTask declares a startup task in the package, so the app can
	// start at login: giode's App.SetLaunchAtLogin turns it on.
	StartupTask bool `toml:"startup_task"`
}

// WindowsWinget configures winget manifests (giode build winget).
type WindowsWinget struct {
	// ID is the package identifier, Publisher.App.
	ID string `toml:"id"`
	// InstallerURL is where users download the setup program; {version}
	// is replaced with the version.
	InstallerURL string `toml:"installer_url"`
	// License is the app's license, as shown by winget.
	License string `toml:"license"`
}

// WindowsSigning signs release builds and installers with an
// Authenticode certificate.
type WindowsSigning struct {
	// Certificate is a .pfx (PKCS#12) file holding the certificate and
	// its private key; PasswordEnv names the environment variable with
	// its password, GIODE_WINDOWS_CERT_PASS by default.
	Certificate string `toml:"certificate"`
	PasswordEnv string `toml:"password_env"`
	// Timestamp is the RFC 3161 timestamp server, so signatures outlive
	// the certificate; DigiCert's by default, "none" for no timestamp.
	Timestamp string `toml:"timestamp"`
	// Command signs with another tool instead, for keys in a hardware
	// token or a cloud service (Azure Trusted Signing, a KMS): the
	// command and its arguments, with {file} replaced by the file to
	// sign in place.
	Command []string `toml:"command"`
}

// Configured reports whether there's a way to sign.
func (s WindowsSigning) Configured() bool {
	return s.Certificate != "" || len(s.Command) > 0
}

// WindowsInstaller configures the setup program giode build
// windows-installer makes.
type WindowsInstaller struct {
	// PerMachine installs for every user into Program Files, asking for
	// administrator rights; by default it installs for the current user
	// without asking.
	PerMachine bool `toml:"per_machine"`
	// License is a text file the installer asks to accept.
	License string `toml:"license"`
	// DesktopShortcut adds a desktop shortcut as well as the Start menu
	// one.
	DesktopShortcut bool `toml:"desktop_shortcut"`
	// Firewall adds an inbound Windows Firewall rule for the app on
	// install (private and domain networks; FirewallPublic adds public
	// ones) and removes it on uninstall, for apps other devices connect
	// to. Per-user installs ask for administrator rights for this step.
	Firewall       bool `toml:"firewall"`
	FirewallPublic bool `toml:"firewall_public"`
	// FirewallPorts limits the MSIX package's rule to these inbound
	// ports: "tcp:53317-53318", "udp:53317". Without them it allows the
	// app on every port, as the setup program's rule does. An MSIX
	// package can't add a rule at install the way the setup program
	// does: it declares them in its manifest, which Firewall (or these
	// ports) turns on.
	FirewallPorts []string `toml:"firewall_ports"`
}

// FirewallPort is an inbound port range of WindowsInstaller.FirewallPorts.
type FirewallPort struct {
	Protocol         string // "TCP" or "UDP"
	PortMin, PortMax int
}

// ParseFirewallPorts parses FirewallPorts.
func (w WindowsInstaller) ParseFirewallPorts() ([]FirewallPort, error) {
	var out []FirewallPort
	for _, s := range w.FirewallPorts {
		proto, ports, ok := strings.Cut(strings.TrimSpace(s), ":")
		proto = strings.ToUpper(proto)
		if !ok || (proto != "TCP" && proto != "UDP") {
			return nil, fmt.Errorf("windows.installer.firewall_ports: %q isn't \"tcp:PORT\" or \"udp:FIRST-LAST\"", s)
		}
		lo, hi, ranged := strings.Cut(ports, "-")
		min, err1 := strconv.Atoi(strings.TrimSpace(lo))
		max := min
		var err2 error
		if ranged {
			max, err2 = strconv.Atoi(strings.TrimSpace(hi))
		}
		if err1 != nil || err2 != nil || min < 1 || max > 65535 || max < min {
			return nil, fmt.Errorf("windows.installer.firewall_ports: %q has no valid port or range", s)
		}
		out = append(out, FirewallPort{Protocol: proto, PortMin: min, PortMax: max})
	}
	return out, nil
}

// Update configures the giode update package for desktop builds.
type Update struct {
	// FeedURL is where the app looks for updates: the feed.json giode
	// release writes, uploaded.
	FeedURL string `toml:"feed_url"`
	// PublicKey is the base64 Ed25519 key the app checks the feed with
	// (giode update-key writes it).
	PublicKey string `toml:"public_key"`
	// PrivateKey is the file with the private key giode release signs
	// with, update.key by default; $GIODE_UPDATE_KEY overrides it with
	// the key itself (for CI).
	PrivateKey string `toml:"private_key"`
}

// UpdateLdflags returns the linker flags configuring the update
// package, or "" without [update].
func (c *Config) UpdateLdflags() string {
	if c.Update.FeedURL == "" || c.Update.PublicKey == "" {
		return ""
	}
	const pkg = "github.com/xmhrnx/giode/update."
	return "-X " + pkg + "defaultFeedURL=" + c.Update.FeedURL + " -X " + pkg + "defaultPublicKey=" + c.Update.PublicKey +
		" -X " + pkg + "defaultVersion=" + c.App.Version
}

// Linux configures Linux builds.
type Linux struct {
	// Categories go in the .desktop file (freedesktop menu categories).
	Categories []string `toml:"categories"`
	// Description is the long description packages and software
	// centers show (app.description is the summary).
	Description string `toml:"description"`
	// Maintainer is "Name <email>" for .deb and .rpm metadata.
	Maintainer string `toml:"maintainer"`
	// License is the app's SPDX license expression.
	License string `toml:"license"`
	// FlatpakPermissions are Flatpak finish-args, replacing the default
	// display, GPU, network and sound access.
	FlatpakPermissions []string `toml:"flatpak_permissions"`
	// AppID replaces app.id for Linux: the Flatpak and AppStream ID,
	// the .desktop file's and icon's names, and the window's Wayland app
	// ID, which the desktop matches to the .desktop file. Flathub takes
	// an ID only from a domain you control, or io.github.<user>.<App>.
	AppID string `toml:"app_id"`
	// Screenshots are shown by software centers, and Flathub requires
	// them: image URLs ("https://example.com/main.png"), or tables with
	// a url and a caption, the first being the main one.
	Screenshots []Screenshot `toml:"screenshots"`
	// Snap configures snaps (giode build snap) for the Snap Store.
	Snap LinuxSnap `toml:"snap"`
}

// LinuxSnap configures snaps.
type LinuxSnap struct {
	// Name is the snap's registered name (snapcraft register): lower
	// case letters, digits and hyphens; the app name made so by
	// default. Setting it adds a snap job to giode ci's workflow.
	Name string `toml:"name"`
	// Plugs are the interfaces the app connects to; by default desktop,
	// desktop-legacy, wayland, x11, opengl, network and audio-playback.
	Plugs []string `toml:"plugs"`
	// Grade is "stable" (default) or "devel", which only reaches the
	// edge and beta channels.
	Grade string `toml:"grade"`
	// Channel is the channel giode ci's workflow releases to: stable
	// by default.
	Channel string `toml:"channel"`
}

// Screenshot is an AppStream screenshot: an image URL and an optional
// caption.
type Screenshot struct {
	URL     string `toml:"url"`
	Caption string `toml:"caption"`
}

// UnmarshalTOML takes a URL alone or a {url, caption} table.
func (s *Screenshot) UnmarshalTOML(v any) error {
	switch v := v.(type) {
	case string:
		s.URL = v
		return nil
	case map[string]any:
		for k, x := range v {
			str, ok := x.(string)
			switch {
			case !ok:
				return fmt.Errorf("linux.screenshots: %s must be a string", k)
			case k == "url":
				s.URL = str
			case k == "caption":
				s.Caption = str
			default:
				return fmt.Errorf("linux.screenshots: unknown key %q (url, caption)", k)
			}
		}
		return nil
	}
	return fmt.Errorf("linux.screenshots: each must be a URL or a {url, caption} table")
}

// LinuxID is the app's ID on Linux: linux.app_id, or app.id.
func (c *Config) LinuxID() string {
	if c.Linux.AppID != "" {
		return c.Linux.AppID
	}
	return c.App.ID
}

// Web configures WebAssembly builds.
type Web struct {
	// Port is where giode run web serves the app, 8080 by default.
	Port int `toml:"port"`
	// PWA makes the site an installable progressive web app that works
	// offline: a manifest, icons and a service worker. True by default.
	PWA *bool `toml:"pwa"`
	// ShortName is the name under the home screen icon (app.name by
	// default); ThemeColor and BackgroundColor CSS colors for the
	// title bar and splash screen.
	ShortName       string `toml:"short_name"`
	ThemeColor      string `toml:"theme_color"`
	BackgroundColor string `toml:"background_color"`
}

// Find looks for giode.toml in dir and its parents.
func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, FileName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s here or in a parent directory (create one with giode init)", FileName)
		}
		dir = parent
	}
}

// Load reads and checks a giode.toml file.
func Load(path string) (*Config, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown settings: %s", path, strings.Join(keys, ", "))
	}
	c.Dir = filepath.Dir(path)
	// A four-part version (1.4.2.7) carries the build number in its
	// fourth part, as Android's version codes do.
	buildSet := md.IsDefined("app", "build")
	if b, ok, err := splitBuild(&c.App.Version); err != nil {
		return nil, fmt.Errorf("%s: app.version: %w", path, err)
	} else if ok {
		c.App.Build, buildSet = b, true
	}
	// CI sets the version from a tag, which may carry the build in a
	// fourth part, and the build number from $GIODE_BUILD (its run
	// counter, unless [ci] build = "tag").
	if v := strings.TrimPrefix(os.Getenv("GIODE_VERSION"), "v"); v != "" {
		c.App.Version = v
		if b, ok, err := splitBuild(&c.App.Version); err != nil {
			return nil, fmt.Errorf("$GIODE_VERSION: %w", err)
		} else if ok {
			c.App.Build, buildSet = b, true
		}
	}
	if b := os.Getenv("GIODE_BUILD"); b != "" {
		n, err := strconv.Atoi(b)
		if err != nil {
			return nil, fmt.Errorf("$GIODE_BUILD %q isn't a number", b)
		}
		c.App.Build, buildSet = n, true
	}
	if buildSet && c.App.Build < 1 {
		return nil, fmt.Errorf("%s: the build number is %d: Android's version code and the stores need it to be at least 1 (app.build, $GIODE_BUILD or a four-part version's last part)", path, c.App.Build)
	}
	// Signing settings CI keeps in secrets rather than the repository.
	if v := os.Getenv("GIODE_IOS_PROFILE"); v != "" {
		c.IOS.Profile = v
	}
	if v := os.Getenv("GIODE_IOS_TEAM_ID"); v != "" {
		c.IOS.TeamID = v
	}
	if v := os.Getenv("GIODE_IOS_SHARE_PROFILE"); v != "" {
		c.IOS.ShareProfile = v
	}
	if v := os.Getenv("GIODE_MACOS_STORE_PROFILE"); v != "" {
		c.MacOS.Store.Profile = v
	}
	if v := os.Getenv("GIODE_MACOS_IDENTITY"); v != "" {
		c.MacOS.Identity = v
	}
	if v := os.Getenv("GIODE_NOTARY_ID"); v != "" {
		c.MacOS.NotaryID = v
	}
	if v := os.Getenv("GIODE_NOTARY_TEAM_ID"); v != "" {
		c.MacOS.NotaryTeamID = v
	}
	c.setDefaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) setDefaults() {
	if c.App.Version == "" {
		c.App.Version = "1.0.0"
	}
	if c.App.Build == 0 {
		c.App.Build = 1
	}
	if c.App.Package == "" {
		c.App.Package = "."
	}
	if c.App.Output == "" {
		c.App.Output = "dist"
	}
	if c.Android.KeystorePassEnv == "" {
		c.Android.KeystorePassEnv = "GIODE_KEYSTORE_PASS"
	}
	if c.MacOS.NotaryPassEnv == "" {
		c.MacOS.NotaryPassEnv = "GIODE_NOTARY_PASS"
	}
	if c.Windows.Signing.PasswordEnv == "" {
		c.Windows.Signing.PasswordEnv = "GIODE_WINDOWS_CERT_PASS"
	}
	if c.Windows.Signing.Timestamp == "" {
		c.Windows.Signing.Timestamp = "http://timestamp.digicert.com"
	}
	if c.App.Publisher == "" {
		c.App.Publisher = c.App.Name
	}
	if c.Web.Port == 0 {
		c.Web.Port = 8080
	}
}

var (
	appIDRE   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
	versionRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// Validate reports settings that would fail a build.
// hexColorRE matches #RRGGBB.
var hexColorRE = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func (c *Config) Validate() error {
	var errs []error
	if c.App.Name == "" {
		errs = append(errs, errors.New("app.name is required"))
	}
	if !appIDRE.MatchString(c.App.ID) {
		errs = append(errs, fmt.Errorf("app.id %q must be a reverse-DNS ID like com.example.notes (letters, digits and _, at least two parts)", c.App.ID))
	}
	if !versionRE.MatchString(c.App.Version) {
		errs = append(errs, fmt.Errorf("app.version %q must be major.minor.patch", c.App.Version))
	}
	if c.App.Build < 1 {
		errs = append(errs, fmt.Errorf("app.build must be at least 1"))
	}
	for _, sc := range []struct{ key, v string }{{"app.splash_color", c.App.SplashColor}, {"app.splash_color_light", c.App.SplashColorLight}} {
		if sc.v != "" && !hexColorRE.MatchString(sc.v) {
			errs = append(errs, fmt.Errorf("%s %q must be a colour like #0A0A0B", sc.key, sc.v))
		}
	}
	if c.App.SplashColorLight != "" && c.App.SplashColor == "" {
		errs = append(errs, errors.New("app.splash_color_light needs app.splash_color, the dark mode colour"))
	}
	if g := c.IOS.AppGroup; g != "" && (!strings.HasPrefix(g, "group.") || strings.ContainsAny(g, " /")) {
		errs = append(errs, fmt.Errorf("ios.app_group %q must look like group.com.example.app", g))
	}
	errs = append(errs, c.validateContextActions()...)
	if _, err := c.Windows.Installer.ParseFirewallPorts(); err != nil {
		errs = append(errs, err)
	}
	if c.Linux.AppID != "" {
		if err := CheckAppID(c.Linux.AppID); err != nil {
			errs = append(errs, fmt.Errorf("linux.app_id: %w", err))
		}
	}
	for i, sc := range c.Linux.Screenshots {
		if !strings.HasPrefix(sc.URL, "https://") && !strings.HasPrefix(sc.URL, "http://") {
			errs = append(errs, fmt.Errorf("linux.screenshots[%d]: %q isn't an image URL (software centers download it)", i, sc.URL))
		}
	}
	errs = append(errs, c.validateFileTypes()...)
	if _, err := c.ParseLinks(); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, c.Privacy.validate()...)
	if cmd := c.Windows.Signing.Command; len(cmd) > 0 && !strings.Contains(strings.Join(cmd, " "), "{file}") {
		errs = append(errs, errors.New("windows.signing.command must contain {file}, where the file to sign goes"))
	}
	for _, p := range c.Android.Permissions {
		if _, err := androidPermission(p); err != nil {
			errs = append(errs, err)
		}
	}
	for _, u := range []map[string]string{c.IOS.Usage, c.MacOS.Usage} {
		for k := range u {
			if _, ok := usageKeys[k]; !ok {
				errs = append(errs, fmt.Errorf("unknown usage %q; known: %s", k, strings.Join(sortedKeys(usageKeys), ", ")))
			}
		}
	}
	for _, p := range []map[string]any{c.IOS.Plist, c.MacOS.Plist} {
		if _, err := plistXML(p); err != nil {
			errs = append(errs, err)
		}
	}
	for _, p := range c.App.Platforms {
		switch p {
		case "windows", "macos", "linux", "android", "ios", "web":
		default:
			errs = append(errs, fmt.Errorf("app.platforms: unknown platform %q (windows, macos, linux, android, ios, web)", p))
		}
	}
	if r := c.App.Renderer; r != "" && r != "opengl" {
		errs = append(errs, fmt.Errorf("app.renderer is %q; the only choice is opengl (leave it out for Gio's default)", r))
	}
	for _, a := range c.CI.Arch {
		if a != "amd64" && a != "arm64" {
			errs = append(errs, fmt.Errorf("ci.arch: %q; giode ci builds amd64 and arm64", a))
		}
	}
	if t := c.CI.Trigger; t != "" && t != "tag" && t != "release" {
		errs = append(errs, fmt.Errorf("ci.trigger is %q; use tag or release", t))
	}
	if b := c.CI.Build; b != "" && b != "run" && b != "tag" {
		errs = append(errs, fmt.Errorf("ci.build is %q; use run or tag", b))
	}
	for k, v := range map[string]string{"ios.push": c.IOS.Push, "macos.push": c.MacOS.Push} {
		if v != "" && v != "development" && v != "production" {
			errs = append(errs, fmt.Errorf("%s is %q; use development or production", k, v))
		}
	}
	for _, d := range c.Android.Dependencies {
		if p := strings.Split(d, ":"); len(p) != 3 || p[0] == "" || p[1] == "" || p[2] == "" {
			errs = append(errs, fmt.Errorf("android.dependencies entry %q isn't group:artifact:version", d))
		}
	}
	return errors.Join(errs...)
}

// Path resolves a path from giode.toml against its directory.
func (c *Config) Path(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Dir, p)
}

// GogioVersion is the app version as gogio takes it:
// major.minor.patch.build.
func (c *Config) GogioVersion() string {
	return fmt.Sprintf("%s.%d", c.App.Version, c.App.Build)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// WindowsCgoEnv is the environment of cgo Windows builds: empty when
// windows.cc isn't set.
func (c *Config) WindowsCgoEnv() []string {
	w := c.Windows
	if w.CC == "" {
		return nil
	}
	env := []string{"CGO_ENABLED=1", "CC=" + w.CC}
	if w.CXX != "" {
		env = append(env, "CXX="+w.CXX)
	}
	if w.PkgConfig != "" {
		env = append(env, "PKG_CONFIG="+w.PkgConfig)
	}
	if w.PkgConfigPath != "" {
		p := w.PkgConfigPath
		if !filepath.IsAbs(p) && !strings.ContainsRune(p, filepath.ListSeparator) {
			p = c.Path(p)
		}
		env = append(env, "PKG_CONFIG_PATH="+p, "PKG_CONFIG_LIBDIR="+p)
	}
	return env
}

// Native configures bundling the shared libraries a cgo app loads -
// libmpv and the libraries it brings - into its packages: next to the
// Windows executable (and in the installer and MSIX), in the macOS app's
// Contents/Frameworks with their install names rewritten, and in a
// Linux package's lib/<command> directory on the binary's run path.
// giode finds them by reading the binary's imports, recursively,
// leaving out the system's.
type Native struct {
	// Bundle turns it on.
	Bundle bool `toml:"bundle"`
	// Exclude are library names (globs) never bundled.
	Exclude []string       `toml:"exclude"`
	Windows NativePlatform `toml:"windows"`
	MacOS   NativePlatform `toml:"macos"`
	Linux   NativeLinux    `toml:"linux"`
}

// NativePlatform configures one platform's libraries.
type NativePlatform struct {
	// Search are directories libraries are looked up in: MSYS2's
	// mingw64/bin, a MinGW sysroot's bin, Homebrew's lib.
	Search []string `toml:"search"`
	// Libraries are bundled besides the imported ones: plugins loaded
	// at run time.
	Libraries []string `toml:"libraries"`
	// Exclude adds to Native.Exclude.
	Exclude []string `toml:"exclude"`
}

// NativeLinux configures Linux's libraries.
type NativeLinux struct {
	NativePlatform
	// System leaves the libraries to the system's packages instead of
	// bundling them in .deb and .rpm packages: DebDepends are added to
	// the .deb's Depends, and the .rpm requires the libraries' sonames
	// (and RPMRequires). AppImages and Flatpaks still bundle them.
	System      bool     `toml:"system"`
	DebDepends  []string `toml:"deb_depends"`
	RPMRequires []string `toml:"rpm_requires"`
}

// FileType is a kind of file the app opens.
type FileType struct {
	// Name describes the type ("MKV video").
	Name string `toml:"name"`
	// Extensions without the dot ("mkv"), and MIME types
	// ("video/x-matroska").
	Extensions []string `toml:"extensions"`
	MIMETypes  []string `toml:"mime_types"`
	// Role is "viewer" (default) or "editor", for macOS.
	Role string `toml:"role"`
	// UTI is the macOS type identifier ("org.matroska.mkv"); when the
	// system doesn't know it, giode declares it from the extensions
	// and MIME types (UTImportedTypeDeclarations).
	UTI string `toml:"uti"`
	// Icon is a document icon for the type (optional; .ico for Windows).
	Icon string `toml:"icon"`
}

// CI configures giode ci's workflow.
type CI struct {
	// Setup are shell commands run before building on each runner,
	// by OS: macos, linux, windows (bash in MSYS2's shell when
	// windows_msys2 is set) - installing the C libraries a cgo app
	// links: "brew install mpv".
	Setup CISetup `toml:"setup"`
	// WindowsMSYS2 builds Windows on Windows runners in MSYS2, for cgo
	// apps: MINGW64 for amd64, CLANGARM64 for arm64 (its packages are
	// ${MINGW_PACKAGE_PREFIX}-name in setup commands).
	WindowsMSYS2 bool `toml:"windows_msys2"`
	// Arch are the CPUs to build Linux and Windows packages for: amd64
	// (the default) and arm64, on GitHub's ARM runners. macOS builds are
	// universal.
	Arch []string `toml:"arch"`
	// Trigger is what starts a release build: "tag" (the default), a
	// pushed version tag, which the workflow then publishes as a
	// release; or "release", a GitHub release being published, which
	// the workflow adds its files to.
	Trigger string `toml:"trigger"`
	// Test runs before any build, on Linux, and a failure stops the
	// release: "go test ./..." by default; "" runs none.
	Test *string `toml:"test"`
	// Build is where the build number comes from: "run" (the default),
	// the workflow's run number; or "tag", the fourth part of a
	// four-part tag (v1.4.2.7), for teams numbering builds themselves.
	Build string `toml:"build"`
	// Dispatch adds a "Run workflow" button (workflow_dispatch) to the
	// Actions tab: on by default with trigger "tag", off with
	// "release", for a pipeline that only runs on releases.
	Dispatch *bool `toml:"dispatch"`
}

// CISetup holds setup commands by runner OS.
type CISetup struct {
	MacOS   string `toml:"macos"`
	Linux   string `toml:"linux"`
	Windows string `toml:"windows"`
}

// NativeOptions returns the library search settings for a platform:
// windows, darwin or linux. Paths may use environment variables
// ($MSYSTEM_PREFIX/bin, ${HOMEBREW_PREFIX}/lib, %LOCALAPPDATA% on
// Windows) and globs (/opt/*/lib/libEGL.dll); a library pattern
// matching nothing is an error, a search directory matching nothing is
// skipped.
func (c *Config) NativeOptions(goos string) (search, libs, exclude []string, err error) {
	var p NativePlatform
	switch goos {
	case "windows":
		p = c.Native.Windows
	case "darwin":
		p = c.Native.MacOS
	default:
		p = c.Native.Linux.NativePlatform
	}
	for _, s := range p.Search {
		for _, m := range c.expandPath(s) {
			if info, err := os.Stat(m); err == nil && info.IsDir() {
				search = append(search, m)
			}
		}
	}
	for _, l := range p.Libraries {
		ms := c.expandPath(l)
		var found []string
		for _, m := range ms {
			if info, err := os.Stat(m); err == nil && !info.IsDir() {
				found = append(found, m)
			}
		}
		if len(found) == 0 {
			return nil, nil, nil, fmt.Errorf("native library %q: no such file (expanded to %s)", l, strings.Join(ms, ", "))
		}
		libs = append(libs, found...)
	}
	return search, libs, append(append([]string{}, c.Native.Exclude...), p.Exclude...), nil
}

var winEnvRE = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_()]*)%`)

// expandPath expands environment variables and globs in a path from
// giode.toml, resolved against its directory, and returns the matches
// (the path itself when it has no glob).
func (c *Config) expandPath(p string) []string {
	p = winEnvRE.ReplaceAllStringFunc(p, func(m string) string { return os.Getenv(m[1 : len(m)-1]) })
	p = os.ExpandEnv(p)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	p = c.Path(filepath.FromSlash(p))
	if strings.ContainsAny(p, "*?[") {
		ms, _ := filepath.Glob(p)
		sort.Strings(ms)
		return ms
	}
	return []string{p}
}

// CheckAppID reports whether id is a valid reverse-DNS app ID.
func CheckAppID(id string) error {
	if !appIDRE.MatchString(id) {
		return fmt.Errorf("an app ID is reverse-DNS, like com.example.notes: letters, digits and _, at least two parts")
	}
	return nil
}

// CheckVersion reports whether v is major.minor.patch.
func CheckVersion(v string) error {
	if !versionRE.MatchString(v) {
		return fmt.Errorf("a version is major.minor.patch, like 1.0.0")
	}
	return nil
}

// splitBuild turns a four-part version ("1.4.2.7") into its first three
// parts and returns the fourth as the build number; other versions are
// left as they are.
func splitBuild(version *string) (build int, ok bool, err error) {
	parts := strings.Split(*version, ".")
	if len(parts) != 4 {
		return 0, false, nil
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil {
		return 0, false, fmt.Errorf("%q: the fourth part (the build number) isn't a number", *version)
	}
	*version = strings.Join(parts[:3], ".")
	return n, true, nil
}
