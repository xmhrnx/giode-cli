# giode CLI

The command-line tool for [giode](https://github.com/xmhrnx/giode) apps: create a
project, run it on your computer, a phone, a simulator or the browser, and build signed
packages and installers for Android, iOS, macOS, Windows, Linux and the web.

```sh
giode new                  # asks for the platforms, name, ID and version, then creates the app
cd my-app                   # the folder you named
giode run                  # on this computer
giode run android          # on a connected phone: build, install, launch, follow the log
giode run windows          # build the Windows app and run it (with Wine off Windows)
giode build android windows-installer web
giode doctor               # what's installed, and what's missing for each target
```

It builds on [gogio](https://gioui.org/doc/install), Gio's packaging tool, whose code it
includes, and adds what gogio can't do: Android manifest entries (services, any
permission, raw XML), Info.plist keys (permission texts, background modes), Windows
installers, Authenticode signing, Linux desktop packaging, a project file instead of
long flag lists, and the run, doctor and device commands.

## Install

**macOS and Linux:**

```sh
curl -fsSL https://raw.githubusercontent.com/xmhrnx/giode-cli/main/install.sh | sh
```

**Windows** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/xmhrnx/giode-cli/main/install.ps1 | iex
```

Both download the latest release for your system and put `giode` on your PATH
(`/usr/local/bin` or `~/.local/bin`; `%LOCALAPPDATA%\Programs\giode` on Windows). Set
`GIODE_VERSION=v0.2.0` for a specific release. Or, with Go installed:

```sh
go install github.com/xmhrnx/giode-cli/cmd/giode@latest
```

which puts `giode` in `$(go env GOPATH)/bin`. `giode self-update` updates a release
install. Building apps needs Go 1.26 or later; `giode doctor` lists what else each
target needs.

## Commands

| Command | What it does |
| --- | --- |
| `giode new [dir]` | Creates a project: a counter app in `main.go`, `go.mod` with the latest giode, `giode.toml` set up for the chosen platforms, placeholder assets (`assets/icon.png`, `assets/README.md`), `.gitignore` and a README. In a terminal it asks first: a checklist of every platform (Windows, macOS, Linux, Android, iOS, Web - whether or not this computer builds them; arrow keys move, Space checks, Enter goes on), then the name, app ID, version, module path, publisher, description and website, each with a suggestion Enter takes. `-y` skips the questions; `-platforms android,ios`, `-id`, `-name` and `-version` answer them. |
| `giode build` (no target) | Builds for this computer's platform: `macos`, `windows` or `linux`, the same as `giode build desktop`. |
| `giode init` | Adds `giode.toml` to an existing Go module. |
| `giode build <target>...` | Builds into `dist/<platform>`. Targets: `android` (APK), `aab` (Play bundle), `ios` (IPA), `ios-sim`, `macos` (universal), `dmg`, `windows`, `windows-installer` (setup program), `msix`, `winget`, `linux`, `deb`, `rpm`, `appimage`, `flatpak`, `web`, `desktop` (this computer) and `all` (everything this computer can build). `-release` signs with your certificates. |
| `giode run [target]` | Builds for a target and runs it: `desktop` (default: `go build`, then the program), `linux`, `windows` (Wine off Windows), `windows-installer`, `macos`, `android` (builds the device's architecture, installs, launches, follows the log), `ios-sim`, `web` (serves `dist/web`). `-mcp` starts giode's MCP server so an AI agent can drive the app. |
| `giode devices` | Lists Android devices and booted iOS simulators. |
| `giode doctor` | Checks Go, the Android SDK, build tools, platform, NDK and JDK, Xcode, and Gio's Linux libraries, with install commands for what's missing. |
| `giode keystore` | Creates an Android release keystore and points `giode.toml` at it. |
| `giode cert` | Creates a self-signed Windows code signing certificate for testing and points `giode.toml` at it. |
| `giode sign <file>...` | Signs Windows executables and installers with the configured certificate. |
| `giode links` | Writes `.well-known/assetlinks.json` and `apple-app-site-association` for each host in `app.links`, to upload to your site (fingerprints from `android.keystore` and `android.fingerprints`, team from `ios.team_id`). |
| `giode update-key` | Creates the Ed25519 key pair signing update feeds: the private key in `update.key` (added to `.gitignore`), the public key in `[update]`. |
| `giode release [targets]` | Builds desktop targets and writes `dist/update/`: the downloads, `feed.json` and its signature, for apps using the giode `update` package. |
| `giode bump [major\|minor\|patch\|build]` | Raises `app.version` and always `app.build`. In CI, `$GIODE_VERSION` (a tag like `v1.4.2`) and `$GIODE_BUILD` override both. |
| `giode ci` | Writes `.github/workflows/release.yml`: on a version tag (or a published release, with `ci.trigger = "release"`), runs the tests, builds packages for the platforms in `app.platforms` (iOS included), signed with the repository's secrets when present, and attaches them to the GitHub release. |
| `giode licenses` | Writes the licenses of the app's Go modules (on any platform) and Go's to `assets/licenses.json` (or `app.licenses`, refreshed on every build) for `giode.LicensesView`. |
| `giode upgrade [version]` | Updates the project's giode dependency and tidies `go.mod`. A project still importing `github.com/mehran9675/giode` is moved to `github.com/xmhrnx/giode`. |
| `giode manifest [android\|ios\|macos]` | Prints what `giode.toml` adds to the manifest and Info.plist. |
| `giode clean` | Removes `dist`. |
| `giode self-update` | Updates the CLI to the latest release. |

Every command takes `-C dir` to run on a project elsewhere, and build commands take `-x`
to print what they run. Targets have short names too: `apk`, `bundle`, `win`, `mac`,
`sim`, `installer`, `browser`.

`build`, `release`, `upgrade`, `self-update`, `licenses` and `clean` print how long they
took when they finish (`Done in 21.3s.`), and `build` and `release` each target's time next
to its output. `run` prints how long the build (and on a device, the install) took before
the app starts.

### What builds where

| Target | Builds on | Needs |
| --- | --- | --- |
| `android`, `aab` | Linux, macOS, Windows | Android SDK with build tools, a platform and the NDK; a JDK |
| `ios`, `ios-sim`, `macos`, `dmg` | macOS | Xcode; for `ios`, a provisioning profile |
| `windows` | Linux, macOS, Windows | nothing extra for pure-Go apps (Gio doesn't need cgo on Windows); a cgo app needs a MinGW-w64 compiler and pkg-config for Windows: `windows.cc` |
| `msix`, `winget` | Linux, macOS, Windows | nothing extra for `msix` (giode writes it); `winget` builds the setup program, so NSIS |
| `windows-installer` | Linux, macOS, Windows | [NSIS](https://nsis.sourceforge.io) (`winget install NSIS.NSIS`, `brew install makensis`, `apt install nsis`) |
| `linux` | Linux | gcc and Gio's Wayland/X11/EGL development packages |
| `deb`, `rpm` | Linux | as `linux`; packages are written by giode itself |
| `appimage` | Linux | as `linux`, plus [appimagetool](https://github.com/AppImage/appimagetool/releases) on PATH |
| `flatpak` | Linux | as `linux`; with `flatpak-builder` a `.flatpak` bundle, without it the manifest |
| `web` | anywhere | nothing extra |

`giode doctor` checks all of these.

## giode.toml

The project file. Only `app.name` and `app.id` are required.

```toml
[app]
name = "Pocket Agent"         # display name
id = "com.example.agent"      # Android package name and Apple bundle ID
version = "1.4.0"
build = 12                    # Android versionCode, CFBundleVersion: at least 1, raise per upload; giode.AppVersion and giode.AppBuild at run time
                              # (a four-part version, "1.4.0.12", sets both)
icon = "assets/icon.png"      # square PNG, 1024x1024 for the stores
package = "."                 # main package
publisher = "Example Ltd"     # shown by installers and Windows' app list
description = "An AI agent in your pocket."
website = "https://example.com"
schemes = ["agent"]           # URL schemes the app opens
links = ["https://example.com/app/*"]   # universal links / App Links; see giode links
tags = ""                     # Go build tags
renderer = "opengl"           # Gio's OpenGL renderer on macOS and iOS (Config.Renderer)
gio_patches = true            # build against Gio with giode's patches (default true)
ldflags = ""                  # extra linker flags; {version}, {build} and {id} are replaced
output = "dist"

[android]
min_sdk = 21
target_sdk = 35
arch = ["arm64", "amd64"]     # default: arm, arm64, 386, amd64
permissions = ["network", "camera", "microphone", "android.permission.VIBRATE"]
keep_alive = true             # declares giode's foreground service for App.KeepAlive
gio_patches = true            # build against Gio with giode's Android fixes (default true)
notifications = true          # POST_NOTIFICATIONS for App.Notify (default true)
queries = ["com.android.chrome"]
manifest = '<uses-feature android:name="android.hardware.camera" android:required="false"/>'
application = '<meta-data android:name="example" android:value="1"/>'
application_attributes = { requestLegacyExternalStorage = "true" }  # on <application>
java = ["android/java"]       # the app's own Java sources, compiled in (see giode/android)
icon_foreground = "assets/android-foreground.png"  # adaptive icon layers (Android 8+): 108dp
icon_background = "#0f172a"   # canvases, artwork in the middle 66dp; background a PNG or color
icon_monochrome = "assets/android-monochrome.png" # themed icons (Android 13+), optional
keystore = "release.keystore" # used by giode build -release
keystore_pass_env = "GIODE_KEYSTORE_PASS"
google_services = "google-services.json"   # from the Firebase console, for giode/push
dependencies = ["com.squareup.okhttp3:okhttp:4.12.0"]  # Maven libraries built in
libraries = ["libs/vendor.aar"]   # local .aar and .jar files built in
# repositories = ["https://dl.google.com/dl/android/maven2", "https://repo1.maven.org/maven2"]

[ios]
min_os = 15
profile = "Agent.mobileprovision"   # for device builds ($GIODE_IOS_PROFILE overrides)
push = "production"           # giode/push: aps-environment (development or production)

[ios.usage]                   # shown when iOS asks for a permission
camera = "To attach photos you take."
microphone = "To dictate messages."
speech = "To dictate messages."

[ios.plist]                   # any other Info.plist keys
UIBackgroundModes = ["audio"]
ITSAppUsesNonExemptEncryption = false

[macos]
min_os = 11
identity = "Developer ID Application: Example Ltd (ABCDE12345)"  # signs -release builds
notary_id = "you@example.com"
notary_team_id = "ABCDE12345"
notary_pass_env = "GIODE_NOTARY_PASS"
# push = "production"         # giode/push; needs profile, a .provisionprofile

[macos.usage]
microphone = "To dictate messages."

[windows]
arch = "amd64"
cc = "x86_64-w64-mingw32-gcc"         # cgo apps: the C compiler for Windows (gcc in MSYS2)
pkg_config_path = "win64/lib/pkgconfig" # the Windows libraries' .pc files
# cxx = "x86_64-w64-mingw32-g++"; pkg_config = "x86_64-w64-mingw32-pkg-config"

[windows.installer]
per_machine = false           # true: all users, Program Files, asks for admin
desktop_shortcut = true
license = "assets/license.txt"

[windows.signing]
certificate = "codesign.pfx"  # password in $GIODE_WINDOWS_CERT_PASS
password_env = "GIODE_WINDOWS_CERT_PASS"
timestamp = "http://timestamp.digicert.com"   # "none" to skip
# command = ["AzureSignTool", "sign", "-kvu", "https://vault.vault.azure.net", "-kvc", "cert", "-kvm", "-tr", "http://timestamp.digicert.com", "{file}"]

[windows.msix]                       # giode build msix
publisher = "CN=Example Ltd"         # the signing certificate's subject (or Partner Center's ID)
identity_name = "ExampleLtd.PocketAgent"   # reserved in Partner Center for the Store
startup_task = true                  # lets the app start at login (App.SetLaunchAtLogin)

[windows.winget]                     # giode build winget
id = "Example.PocketAgent"
installer_url = "https://example.com/download/PocketAgent-{version}-{arch}-setup.exe"
license = "MIT"

[linux]
categories = ["Utility", "Office"]
description = "Longer text for package managers and software centers."
maintainer = "Jane Doe <jane@example.com>"   # .deb/.rpm metadata
license = "MIT"                              # SPDX expression
# flatpak_permissions = ["--share=ipc", "--socket=wayland", "--device=dri"]

[web]
port = 8080
pwa = true                    # installable, works offline (default)
short_name = "Agent"          # under the home screen icon
theme_color = "#111827"
background_color = "#111827"

# Self-updates for desktop builds (the giode update package); giode update-key
# writes public_key, giode release the signed feed to upload to feed_url.
[update]
feed_url = "https://example.com/updates/feed.json"
public_key = "…"

# The privacy manifest (PrivacyInfo.xcprivacy) App Store apps need. giode always
# declares the system boot time and file timestamp APIs Go itself uses.
[privacy]
tracking = false
[[privacy.collected]]
type = "crash_data"           # Apple's data types, by short or full name
linked = false
purposes = ["app_functionality"]
[privacy.api]
user_defaults = ["CA92.1"]    # more required-reason APIs your code uses

# Entries in the file manager's right-click menu that open the app with the
# chosen paths (giode.Config.OnContextAction receives them).
[[context_actions]]
id = "ask"
label = "Ask Pocket Agent"
extensions = [".md", ".txt"]  # or files = true for every file

[[context_actions]]
id = "here"
label = "Open Pocket Agent here"
folders = true
background = true             # the empty space of an open folder
```

**Android permissions** take gogio's short names (`network`, `networkstate`,
`bluetooth`, `camera`, `storage`, `wakelock`, `microphone`), giode's (`notifications`,
`foreground_service`, `vibrate`, `biometric`, `location`, `coarse_location`,
`contacts`, `calendar`, `boot`) or full `android.permission.X` names. Importing
`gioui.org/app/permission/...` packages still adds theirs, as with gogio.

**iOS usage texts** take short names: `camera`, `microphone`, `speech`, `photos`,
`photos_add`, `location`, `location_always`, `contacts`, `calendar`, `reminders`,
`bluetooth`, `faceid`, `motion`, `health`, `health_update`, `tracking`,
`local_network`. iOS ends an app that uses the camera, microphone or speech recognition
without one; giode's `PickPhoto` and `Dictate` check for them and return an error instead.

**Context actions** go into the build (the giode library reads them, and
`App.InstallContextActions` registers them for the current user), the Windows installer
(Explorer's context menu, removed on uninstall; on Windows 11 under "Show more options"),
and the macOS app's `NSServices` (Finder's Quick Actions, once the app is in
`/Applications` and has run once).

## Signing

`giode build -release` builds for distribution and signs everything it can with the
certificates in `giode.toml`; without `-release`, Android uses the debug key and the
rest are unsigned.

| Platform | What signs it | Set up |
| --- | --- | --- |
| Android | `android.keystore` | `GIODE_KEYSTORE_PASS=... giode keystore` creates one. Keep it and its password safe: Google Play only accepts updates signed with it (with Play App Signing, it's your upload key). |
| Windows | `windows.signing`: an Authenticode `.pfx`, or a `command` | Signs the executable and the setup program, with a timestamp so signatures outlive the certificate. Uses `signtool` on Windows and `osslsigncode` elsewhere. For keys in a hardware token or a cloud service (Azure Trusted Signing, a KMS), give `command` with `{file}` where the file goes. `giode cert` makes a self-signed certificate to test with; for public releases, use one from a certificate authority so SmartScreen doesn't warn. |
| macOS | `macos.identity` (or `macos.profile`) | A "Developer ID Application" identity in your keychain. With `macos.notary_id` and the app-specific password in `$GIODE_NOTARY_PASS`, the app is also notarized, so Gatekeeper opens it without warnings. |
| iOS | `ios.profile` | Device builds always need a provisioning profile; the matching identity must be in your keychain. |

`giode sign <file>...` signs other Windows files the same way.

## Windows installer

`giode build windows-installer` makes `dist/windows/<App>-<version>-<arch>-setup.exe` with NSIS:
a wizard (welcome, optional license, folder, progress, finish with a "Run" box), a Start
menu shortcut (and a desktop one with `desktop_shortcut`), an entry in Settings > Apps
with publisher, version and size, and an uninstaller. By default it installs for the
current user into `%LOCALAPPDATA%\Programs`, without an administrator prompt;
`per_machine = true` installs for everyone into Program Files. It runs silently with
`/S`. The uninstaller isn't signed yet.

## Patched Gio

`giode build` and `giode run` build against a copy of your Gio version with giode's
patches, adding the `giode_gio` and `giode_gio2` build tags:

- **Underlay hooks**, on every platform: what giode's `Config.Underlay`,
  `Config.UnderlayRelease` and `Config.Renderer` use to draw video or 3D under the UI. The
  window calls the hook with the frame's render target before drawing the UI over it
  without clearing, calls another once the UI is drawn (the MCP screenshot reads the frame
  back there) and a third before releasing its GPU resources; Gio's OpenGL renderer saves
  and restores the GL state around its frames, and on Windows it can prefer OpenGL ES
  (ANGLE) over Direct3D. `renderer = "opengl"` under `[app]` adds the `nometal` tag to
  macOS and iOS builds, for Gio's OpenGL renderer there.
- **File drags on Linux**: XDND on X11 and `wl_data_device`'s drag and drop on Wayland,
  for `Config.OnFileDrop` and `OnFileDrag`. Gio's event loop reads those protocols'
  messages, so they can't be handled outside it.
- **Minimized windows on Windows**: Gio took a minimized window for a restored one, so
  `WindowMinimized`, desktop `OnBackground` and the way back from them never came.
- **Fullscreen on Windows**: Gio showed a fullscreen window maximized without its frame,
  and Windows draws the taskbar over maximized windows. The patched copy makes it a popup
  covering the monitor, and puts it back where and as it was when leaving fullscreen.
- **Fullscreen on macOS**: Gio wrote the window's style mask back after starting AppKit's
  fullscreen transition, setting the fullscreen bit again in the middle of leaving
  fullscreen, and resized the window during the transition; the window's mode was read
  again only when it drew, so a window that wasn't drawing kept its old mode. The patched
  copy leaves the fullscreen bit to AppKit, doesn't resize during transitions, reports the
  mode when a transition ends, and puts a window back to the normal level when `TopMost`
  is turned off.
- **Input from automation**: the MCP server's `ui_pointer` and `ui_key` deliver pointer
  and key events to the window's input as the platform would.

And two Android fixes Gio hasn't released:

- **Full refresh rate.** Gio asks Android's Choreographer for the next frame only after
  presenting the current one. Presenting waits for a vsync, so the request misses the next
  one, and a 120Hz display gets 60 frames a second while anything animates. The patched
  copy asks before drawing, and drops a frame callback that arrives after the animation
  stopped instead of drawing a frame for nothing.
- **Held keys repeat.** After every edit Gio restarts the soft keyboard's input connection
  if the editor's snippet of text differs from the keyboard's - which it does after any
  edit, since the editor's snippet window moves with the text. The restart cancels the
  keyboard's key repeat, so holding backspace deleted one character. The patched copy
  restarts only when the text changed under the keyboard (an app clearing or replacing the
  field), not when the editor just moved its window over text the keyboard already has.

Your `go.mod` isn't touched. The copy is made once per Gio version in the user cache
directory (`giode/gio/<version>-giode<n>`, made again when the patches change), and the build gets a temporary `go.mod` with
`replace gioui.org => <copy>`, passed with `-modfile` to the `go build` and `go list`
commands run in your module (`GOFLAGS` is left alone). The patch is skipped, with a note,
when go isn't in module mode, or your module already replaces `gioui.org`, vendors its
dependencies or is in a Go workspace, or when your Gio version doesn't have the code the patch expects. Set
`gio_patches = false` under `[app]` to build against plain Gio (or under `[android]` for
Android builds only).

## Native libraries

A cgo app linking shared libraries (libmpv and the dozens it brings) needs them in its
packages. With `[native] bundle = true`, giode reads the built binary's imports
(PE, ELF or Mach-O, recursively, so it works when cross-building), leaves out the
system's (Windows' DLLs, macOS' `/usr/lib` and `/System`, a Linux desktop's C library,
graphics, windowing and sound stacks) and bundles the rest:

| Target | Where they go |
| --- | --- |
| `windows`, `windows-installer`, `msix`, `winget` | next to the `.exe`, installed (and uninstalled) with it |
| `macos`, `dmg` | `Contents/Frameworks`, renamed `@rpath/<name>` with every reference rewritten and the executable given the run path (as `dylibbundler` does), signed with the app |
| `linux`, `appimage`, `flatpak`, `deb`, `rpm` | `lib/` next to the binary, or the package's `lib/<command>`; the binary's run path finds them |

```toml
[native]
bundle = true
exclude = ["libvulkan*"]              # never bundled

[native.windows]
search = ["$MSYSTEM_PREFIX/bin"]      # where the DLLs are (MSYS2, a MinGW sysroot)
libraries = ["$MSYSTEM_PREFIX/bin/libEGL*.dll"]  # also bundled

[native.macos]
search = ["/opt/homebrew/lib"]

[native.linux]
system = true                         # .deb and .rpm depend on the system's packages instead
deb_depends = ["libmpv2"]             # the .rpm requires the libraries' sonames
libraries = ["/usr/lib/x86_64-linux-gnu/libva-drm.so.2"]  # also bundled: plugins loaded at run time
```

Paths in `search` and `libraries` may use environment variables (`$VAR`, `${VAR}`,
`%VAR%`), `~/` and globs; relative paths are from giode.toml's folder. A `libraries`
pattern that matches nothing is an error; a `search` folder that doesn't exist is skipped.
Windows' imports include delay-loaded DLLs, and the Visual C++ runtime
(`vcruntime140.dll`, `msvcp140.dll` and the rest) is bundled when found but not warned
about when it isn't.

With `renderer = "opengl"`, Windows builds need ANGLE's `libEGL.dll` and
`libGLESv2.dll`, which the binary loads at run time rather than imports. giode bundles
them without `[native]`: it looks in `[native.windows] search`, `$MSYSTEM_PREFIX/bin`,
MSYS2's `clangarm64`, `clang64`, `ucrt64` and `mingw64` (under `$MSYS2_ROOT` or
`C:\msys64`) and Visual Studio Code's install, taking the first pair built for the target
CPU, along with the DLLs they import. `pacman -S mingw-w64-x86_64-angleproject` (or
`mingw-w64-clang-aarch64-angleproject`) installs them.

Libraries a binary imports that aren't the system's and aren't found are listed in a
warning; on Windows that leaves out DLLs in `System32` (when building on Windows) and the
graphics driver's (`vulkan-1.dll`, `OpenCL.dll`). A program's own folder - the output
folder, with the last build's copies - isn't searched for its DLLs, so a rebuild takes
them from the search folders again. `giode doctor` checks the tools: `install_name_tool` and `codesign` on macOS,
`windows.cc` and pkg-config for Windows cgo builds.

## File associations

`[[file_types]]` puts the app in "Open with" for those files on every desktop:

```toml
[[file_types]]
name = "MKV video"
extensions = ["mkv", "mk3d"]
mime_types = ["video/x-matroska"]
# uti = "org.matroska.mkv"   # macOS type; giode declares one from the extensions when unset
# role = "editor"            # macOS; viewer by default

[[file_types]]
name = "Player playlist"
extensions = ["gpl"]          # no MIME type: giode declares application/x-<command>-gpl on Linux
```

- macOS: `CFBundleDocumentTypes` in Info.plist, and `UTImportedTypeDeclarations` for the
  types giode declares. The files arrive as `file://` URLs through `Config.OnOpenURL`.
- Windows: the setup program registers a ProgID per type, `OpenWithProgids` for each
  extension and the app's `SupportedTypes`, and removes them on uninstall; MSIX declares
  `windows.fileTypeAssociation`. The file's path is the program's argument.
- Linux: the `.desktop` file's `MimeType`, and a shared-mime-info package for types only
  the app knows. The file's path is the program's argument.

## CI for cgo apps

`giode ci`'s workflow installs what Gio needs; a cgo app's C libraries come from
`[ci] setup`, commands run on each runner before building. `windows_msys2` builds the
Windows packages on a Windows runner in MSYS2's MINGW64 environment, where libraries like
libmpv are a `pacman` away, instead of cross-building them on Linux:

```toml
[ci]
windows_msys2 = true

[ci.setup]
macos = "brew install mpv"
linux = "sudo apt-get install -y libmpv-dev"
windows = "pacman -S --noconfirm ${MINGW_PACKAGE_PREFIX}-mpv"
```

`arch` builds each desktop's packages for more CPUs. With `arm64`, Linux packages are
also built on `ubuntu-24.04-arm`, and Windows ones cross-built for arm64 (or, with
`windows_msys2`, built on `windows-11-arm` in MSYS2's CLANGARM64 environment, where
`$MINGW_PACKAGE_PREFIX` is `mingw-w64-clang-aarch64`). macOS' dmg is universal already.

```toml
[ci]
arch = ["amd64", "arm64"]   # default ["amd64"]
```

The workflow builds the platforms in `app.platforms` (without it, all but iOS). An iOS job
builds an IPA when the repository has the `IOS_CERT_BASE64`, `IOS_CERT_PASS` and
`IOS_PROFILE_BASE64` secrets (`IOS_TEAM_ID` sets `ios.team_id`), and checks the simulator
build otherwise; the profile reaches the build through `$GIODE_IOS_PROFILE`, so giode.toml
needn't name it. A test job runs first and stops the release when it fails. Change what it
runs, or turn it off, with `test`. `trigger = "release"` builds when a GitHub release is
published, adding the files to it, instead of on a pushed tag:

```toml
[ci]
trigger = "release"                    # default "tag"
test = "go vet ./... && go test ./..." # default "go test ./..."; "" runs none
build = "tag"                          # default "run": the build number is the run number
dispatch = false                       # default true for "tag", false for "release"
```

The tag sets the version. A four-part tag (`v1.4.2.7`) also carries the build number in its
last part; with `build = "tag"` the workflow uses it instead of the run number. `dispatch`
adds or removes the Actions tab's "Run workflow" button. App bundles (`giode build aab`)
need bundletool, which the SDK doesn't include: giode uses one from the build tools folder,
or downloads bundletool 1.17.2 into its cache on the first bundle build, so CI runners need
nothing extra.

Rerun `giode ci -f` after changing `[ci]` rather than editing the workflow: it's written
from giode.toml and overwritten.

Every package's name carries its CPU, so builds for two CPUs can share a release:
`<App>-<version>-<arch>-setup.exe`, `<App>-<version>-<arch>.msix` and
`<App>-<version>-universal.dmg` (or `-arm64`/`-amd64` with a single `-arch`).

## Android icon

Android 8 and later draw the launcher icon from two layers, each a 108dp square of which
the launcher shows only the middle, cut to its own shape (a circle, a squircle, ...). Keep
the artwork inside the middle 66dp. Without settings, giode builds them from `app.icon`: the
icon shrunk into that safe zone on a transparent foreground, over the color around its edge
(white for a transparent edge), so a square icon isn't clipped. To draw the layers
yourself:

```toml
[android]
icon_foreground = "assets/android-foreground.png" # the whole 108dp canvas, transparent around the artwork
icon_background = "assets/android-background.png" # or a color: "#0f172a"
icon_monochrome = "assets/android-monochrome.png" # optional: its shape is tinted for themed icons (Android 13+)
```

Without `icon_monochrome`, themed icons show the full-color icon. Android 7 and earlier
use `app.icon` as is.

## Android libraries

Android builds take libraries from Maven repositories, as Gradle would:
`android.dependencies` lists `group:artifact:version` coordinates, which
giode resolves with their dependencies from Google's Maven repository and
Maven Central (cached in the user cache directory), and
`android.libraries` adds local `.aar` and `.jar` files. Their classes are
dexed with the app's (multidex when needed), their resources linked with
R classes for their packages, their manifests merged into the app's, and
their native libraries (for the app's ABIs) and assets packaged.

A Go package can ask for libraries itself: an `android_dependencies.txt`
next to its Go files lists coordinates, and an `android_manifest.xml`
adds permissions and `<application>` components, for every app importing
it. That's how giode's `push` (Firebase Cloud Messaging), `store` (Play
Billing) and `review` (Play In-App Review) packages bring what they need;
`push` also needs `android.google_services`, the `google-services.json`
file from the Firebase console, which giode turns into the resources
Firebase reads, as Google's Gradle plugin does.

## License

Apache License 2.0; see [LICENSE](LICENSE). `internal/gogio` is derived from gioui.org's
gogio and stays under its Unlicense/MIT terms; see [NOTICE](NOTICE).
