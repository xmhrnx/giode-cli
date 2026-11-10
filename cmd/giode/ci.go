package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/xmhrnx/giode-cli/internal/config"
)

const bumpUsage = `
Usage: giode bump [major|minor|patch|build]

Raises app.version in giode.toml - major (1.4.2 to 2.0.0), minor (1.5.0)
or patch (1.4.3) - and always app.build, the build number stores require
to grow with every upload. "build" raises only the build number.

In CI, $GIODE_VERSION (a tag like v1.4.2) and $GIODE_BUILD (the run
number) override both without editing the file.
`

func cmdBump(args []string) error {
	fs := flags("bump", bumpUsage)
	dir := fs.String("C", ".", "project directory")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	part := "build"
	if len(pos) > 0 {
		part = pos[0]
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	v := strings.Split(c.App.Version, ".")
	for len(v) < 3 {
		v = append(v, "0")
	}
	n := make([]int, 3)
	for i := range n {
		n[i], _ = strconv.Atoi(v[i])
	}
	switch part {
	case "major":
		n = []int{n[0] + 1, 0, 0}
	case "minor":
		n = []int{n[0], n[1] + 1, 0}
	case "patch":
		n[2]++
	case "build":
	default:
		return fmt.Errorf("bump %q: say major, minor, patch or build", part)
	}
	version := fmt.Sprintf("%d.%d.%d", n[0], n[1], n[2])
	build := c.App.Build + 1
	path := filepath.Join(c.Dir, config.FileName)
	if err := setAppValues(path, version, build); err != nil {
		return err
	}
	fmt.Printf("app.version %s, app.build %d\n", version, build)
	return nil
}

var (
	versionLine = regexp.MustCompile(`(?m)^version\s*=\s*"[^"]*"`)
	buildLine   = regexp.MustCompile(`(?m)^build\s*=\s*\d+`)
)

// setAppValues sets version and build in giode.toml's [app] table.
func setAppValues(path, version string, build int) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(b)
	start := strings.Index(s, "[app]\n")
	if start < 0 {
		return errors.New("giode.toml has no [app] table")
	}
	end := len(s)
	if i := strings.Index(s[start+6:], "\n["); i >= 0 {
		end = start + 6 + i + 1
	}
	table := s[start:end]
	if versionLine.MatchString(table) {
		table = versionLine.ReplaceAllString(table, fmt.Sprintf("version = %q", version))
	} else {
		table = strings.Replace(table, "[app]\n", fmt.Sprintf("[app]\nversion = %q\n", version), 1)
	}
	if buildLine.MatchString(table) {
		table = buildLine.ReplaceAllString(table, fmt.Sprintf("build = %d", build))
	} else {
		table = versionLine.ReplaceAllStringFunc(table, func(m string) string { return m + fmt.Sprintf("\nbuild = %d", build) })
	}
	return os.WriteFile(path, []byte(s[:start]+table+s[end:]), 0o644)
}

const ciUsage = `
Usage: giode ci

Writes .github/workflows/release.yml: on a version tag (v1.4.2) it tests
the app, builds it for the platforms in app.platforms - Linux (deb, rpm),
Windows (setup program, MSIX), Android (APK, AAB), macOS (DMG), iOS
(IPA) and the web; without app.platforms, all of them but iOS - signing
with what your repository's secrets provide, and attaches everything to a
GitHub release. The tag sets the version and the run number the build
number.

In giode.toml, [ci] trigger = "release" builds when a GitHub release is
published instead, adding the files to it; test sets the commands run
before building ("go test ./..." by default, "" for none); setup runs
commands before building on each runner (installing the C libraries a
cgo app links); and windows_msys2 builds the Windows packages on a
Windows runner with MSYS2's MINGW64 toolchain:

	[ci]
	trigger = "release"
	build = "tag"                   # build number from v1.4.2.7's last part, not the run number
	dispatch = false                # no "Run workflow" button (the default with trigger = "release")
	test = "go vet ./... && go test ./..."
	windows_msys2 = true
	arch = ["amd64", "arm64"]       # builds per CPU: Linux on ubuntu-24.04-arm,
	                               # Windows on windows-11-arm (CLANGARM64)
	[ci.setup]
	macos = "brew install mpv"
	linux = "sudo apt-get install -y libmpv-dev"
	windows = "pacman -S --noconfirm ${MINGW_PACKAGE_PREFIX}-mpv"

Rerun giode ci -f after changing these rather than editing the workflow,
which it overwrites.

Secrets it uses when set (Settings > Secrets and variables > Actions):

	ANDROID_KEYSTORE_BASE64, GIODE_KEYSTORE_PASS   Android release keystore
	WINDOWS_CERT_BASE64, GIODE_WINDOWS_CERT_PASS   Windows .pfx certificate
	MACOS_CERT_BASE64, MACOS_CERT_PASS             macOS Developer ID .p12
	GIODE_NOTARY_ID                                notarization Apple ID (macos.notary_id)
	GIODE_NOTARY_TEAM_ID                           its team ID (default: the certificate's)
	GIODE_NOTARY_PASS                              its app-specific password
	IOS_CERT_BASE64, IOS_CERT_PASS                 iOS distribution .p12
	IOS_PROFILE_BASE64                             iOS provisioning profile
	IOS_SHARE_PROFILE_BASE64                       iOS share extension's profile (app.share_target)
	IOS_TEAM_ID                                    Apple team ID (ios.team_id)
	GIODE_UPDATE_KEY                               update feed key (giode release)
	SNAPCRAFT_STORE_CREDENTIALS                    Snap Store upload (snapcraft export-login)
	MACOS_STORE_CERT_BASE64, MACOS_STORE_CERT_PASS  Apple Distribution .p12 (macos.store)
	MACOS_INSTALLER_CERT_BASE64, MACOS_INSTALLER_CERT_PASS
	                                               Mac installer .p12 (3rd Party Mac Developer Installer)
	MACOS_PROFILE_BASE64                           Mac App Store provisioning profile

Without the iOS certificate and profile (and, with app.share_target, the
share extension's profile) the iOS job builds for the simulator only.
`

func cmdCI(args []string) error {
	fs := flags("ci", ciUsage)
	dir := fs.String("C", ".", "project directory")
	force := fs.Bool("f", false, "overwrite an existing workflow")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	path := filepath.Join(c.Dir, ".github", "workflows", "release.yml")
	if _, err := os.Stat(path); err == nil && !*force {
		return fmt.Errorf("%s exists; use -f to overwrite it", relPath(path))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(workflow(c)), 0o644); err != nil {
		return err
	}
	if c.CI.Trigger == "release" {
		fmt.Printf("Wrote %s. Publish a release on GitHub (tag v%s) to build and attach the packages.\n", relPath(path), c.App.Version)
	} else {
		fmt.Printf("Wrote %s. Push a tag to release: git tag v%s && git push --tags\n", relPath(path), c.App.Version)
	}
	return nil
}

// workflow returns the release workflow for the project.
func workflow(c *config.Config) string {
	var b strings.Builder
	if err := workflowTemplate.Execute(&b, newCIData(c)); err != nil {
		panic(err) // the template is fixed; tests run it
	}
	return b.String()
}

// ciRunner is one architecture's runner for a job.
type ciRunner struct {
	Arch, Runner string
	// Primary builds the targets made once per release (web, Android,
	// Windows when cross-built).
	Primary bool
	// MSYS2 environment, its compiler, and NSIS from MSYS2 or Chocolatey.
	MSYSTEM, CC string
	ChocoNSIS   bool
}

type ciData struct {
	Keystore, Cert                 string
	Linux, Windows                 []ciRunner
	CrossWindows                   []string
	WindowsMSYS2                   bool
	SetupLinux, SetupMac, SetupWin string
	Needs                          string
	// PublishIf is the publish job's condition: a failed snap job
	// (the Snap Store can be slow) doesn't hold back the release.
	PublishIf string

	// Release builds when a GitHub release is published, rather than
	// on a pushed tag.
	Release bool
	// BuildFromTag takes the build number from a four-part tag rather
	// than the run number; Dispatch adds workflow_dispatch.
	BuildFromTag, Dispatch bool
	// Test holds the test commands, indented; empty runs no test job.
	Test string
	// The platforms built: Linux packages, the web, Android, macOS,
	// iOS. LinuxJob is whether the Linux job has anything to build.
	LinuxPkgs, Web, Android, MacOS, IOS, LinuxJob bool
	// ShareTarget is app.share_target: a device build for iOS also needs
	// the share extension's profile.
	ShareTarget bool
	// Snap builds a snap per Linux runner and uploads it to the Snap
	// Store on a release (linux.snap.name set), to SnapChannel.
	Snap        bool
	SnapChannel string
	// MAS builds the Mac App Store package too (macos.store set).
	MAS bool
}

func newCIData(c *config.Config) ciData {
	archs := c.CI.Arch
	if len(archs) == 0 {
		archs = []string{"amd64"}
	}
	// Without app.platforms, everything but iOS, which needs signing
	// set up first.
	has := func(p string) bool {
		if len(c.App.Platforms) == 0 {
			return p != "ios"
		}
		for _, q := range c.App.Platforms {
			if q == p {
				return true
			}
		}
		return false
	}
	test := "go test ./..."
	if c.CI.Test != nil {
		test = *c.CI.Test
	}
	d := ciData{
		Keystore:     orDefault(c.Android.Keystore, "release.keystore"),
		Cert:         orDefault(c.Windows.Signing.Certificate, "codesign.pfx"),
		WindowsMSYS2: c.CI.WindowsMSYS2 && has("windows"),
		SetupLinux:   setupIndent(c.CI.Setup.Linux, "          "),
		SetupMac:     setupIndent(c.CI.Setup.MacOS, "          "),
		SetupWin:     setupIndent(c.CI.Setup.Windows, "          "),
		Release:      c.CI.Trigger == "release",
		BuildFromTag: c.CI.Build == "tag",
		Test:         setupIndent(test, "          "),
		LinuxPkgs:    has("linux"),
		Web:          has("web"),
		Android:      has("android"),
		MacOS:        has("macos"),
		IOS:          has("ios"),
		ShareTarget:  c.App.ShareTarget,
	}
	for i, a := range archs {
		runner := "ubuntu-latest"
		if a == "arm64" {
			runner = "ubuntu-24.04-arm"
		}
		// Only Linux packages are built per CPU on Linux runners.
		if i == 0 || d.LinuxPkgs {
			d.Linux = append(d.Linux, ciRunner{Arch: a, Runner: runner, Primary: i == 0})
		}
		if !has("windows") {
			continue
		}
		if d.WindowsMSYS2 {
			w := ciRunner{Arch: a, Runner: "windows-latest", MSYSTEM: "MINGW64", CC: "gcc"}
			if a == "arm64" {
				w = ciRunner{Arch: a, Runner: "windows-11-arm", MSYSTEM: "CLANGARM64", CC: "clang", ChocoNSIS: true}
			}
			d.Windows = append(d.Windows, w)
		} else {
			d.CrossWindows = append(d.CrossWindows, a)
		}
	}
	d.Dispatch = !d.Release
	if c.CI.Dispatch != nil {
		d.Dispatch = *c.CI.Dispatch
	}
	d.LinuxJob = d.LinuxPkgs || d.Web || d.Android || len(d.CrossWindows) > 0
	d.Snap = d.LinuxPkgs && c.Linux.Snap.Name != ""
	st := c.MacOS.Store
	d.MAS = d.MacOS && (len(st.Entitlements) > 0 || st.Profile != "" || st.InstallerIdentity != "")
	d.SnapChannel = orDefault(c.Linux.Snap.Channel, "stable")
	var needs []string
	for _, j := range []struct {
		name string
		on   bool
	}{{"linux", d.LinuxJob}, {"snap", d.Snap}, {"macos", d.MacOS}, {"windows", d.WindowsMSYS2}, {"ios", d.IOS}} {
		if j.on {
			needs = append(needs, j.name)
		}
	}
	d.Needs = strings.Join(needs, ", ")
	d.PublishIf = "github.ref_type == 'tag'"
	if d.Snap {
		conds := []string{"!cancelled()", d.PublishIf}
		for _, n := range needs {
			if n != "snap" {
				conds = append(conds, "needs."+n+".result == 'success'")
			}
		}
		d.PublishIf = "${{ " + strings.Join(conds, " && ") + " }}"
	}
	return d
}

// setupIndent indents setup commands for a run block; empty stays empty.
func setupIndent(cmds, prefix string) string {
	cmds = strings.TrimRight(cmds, "\n")
	if strings.TrimSpace(cmds) == "" {
		return ""
	}
	return prefix + strings.ReplaceAll(cmds, "\n", "\n"+prefix)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

var workflowTemplate = template.Must(template.New("release").Delims("[[", "]]").Parse(releaseWorkflow))

// releaseWorkflow is the GitHub Actions workflow giode ci writes.
const releaseWorkflow = `# Written by giode ci from giode.toml; rerun giode ci -f after changing
# [ci] there rather than editing this file.
[[- if .Release]]
# Builds when a GitHub release is published and adds the files to it.
[[- else]]
# Builds and publishes a release when a version tag is pushed.
[[- end]]
name: release

on:
[[- if .Release]]
  release:
    types: [published]
[[- else]]
  push:
    tags: ["v*"]
[[- end]]
[[- if .Dispatch]]
  workflow_dispatch:
[[- end]]

permissions:
  contents: write

env:
  # A four-part tag (v1.4.2.7) also sets the build number.
  GIODE_VERSION: ${{ github.ref_type == 'tag' && github.ref_name || '' }}
[[- if not .BuildFromTag]]
  GIODE_BUILD: ${{ github.run_number }}
[[- end]]

jobs:
[[- if .Test]]
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 60
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - name: Install build dependencies
        run: |
          sudo apt-get update
          sudo apt-get install -y gcc pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libvulkan-dev
[[- if .SetupLinux]]
      - name: Project setup (giode.toml ci.setup)
        run: |
[[.SetupLinux]]
[[- end]]
      - name: Test (giode.toml ci.test)
        run: |
[[.Test]]
[[- end]]
[[- if .LinuxJob]]

  linux:
[[- if .Test]]
    needs: test
[[- end]]
    strategy:
      matrix:
        include:[[range .Linux]]
          - arch: [[.Arch]]
            runner: [[.Runner]]
            primary: [[.Primary]][[end]]
    runs-on: ${{ matrix.runner }}
    timeout-minutes: 60
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - name: Install build dependencies
        run: |
          sudo apt-get update
          sudo apt-get install -y gcc pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libvulkan-dev nsis osslsigncode
          go install github.com/xmhrnx/giode-cli/cmd/giode@latest
[[- if .SetupLinux]]
      - name: Project setup (giode.toml ci.setup)
        run: |
[[.SetupLinux]]
[[- end]]
      - name: Signing keys
        env:
          ANDROID_KEYSTORE_BASE64: ${{ secrets.ANDROID_KEYSTORE_BASE64 }}
          WINDOWS_CERT_BASE64: ${{ secrets.WINDOWS_CERT_BASE64 }}
        run: |
          if [ -n "$ANDROID_KEYSTORE_BASE64" ]; then echo "$ANDROID_KEYSTORE_BASE64" | base64 -d > [[.Keystore]]; fi
          if [ -n "$WINDOWS_CERT_BASE64" ]; then echo "$WINDOWS_CERT_BASE64" | base64 -d > [[.Cert]]; fi
[[- if .LinuxPkgs]]
      - name: Build Linux packages
        run: giode build deb rpm
[[- end]]
[[- if or .Web .CrossWindows .Android]]
      - name: Build[[if .Web]] web[[end]][[if .CrossWindows]] Windows[[end]][[if .Android]] Android[[end]]
        if: matrix.primary
        env:
          GIODE_KEYSTORE_PASS: ${{ secrets.GIODE_KEYSTORE_PASS }}
          GIODE_WINDOWS_CERT_PASS: ${{ secrets.GIODE_WINDOWS_CERT_PASS }}
        run: |
[[- if .Web]]
          giode build web
[[- end]]
[[- if .CrossWindows]]
          # Signed when the repository has the certificate or keystore.
          win=""; if [ -n "$GIODE_WINDOWS_CERT_PASS" ]; then win="-release"; fi
[[- range .CrossWindows]]
          giode build $win -arch [[.]] windows-installer msix
[[- end]]
[[- end]]
[[- if .Android]]
          apk=""; if [ -n "$GIODE_KEYSTORE_PASS" ]; then apk="-release"; fi
          giode build $apk android aab
[[- end]]
[[- end]]
      - uses: actions/upload-artifact@v4
        with:
          name: linux-${{ matrix.arch }}
          if-no-files-found: ignore
          path: |
            dist/linux/*.deb
            dist/linux/*.rpm
            dist/windows/*.exe
            dist/windows/*.msix
            dist/android/*.apk
            dist/android/*.aab
            dist/android/native-debug-symbols.zip
[[- end]]
[[- if .Snap]]

  snap:
[[- if .Test]]
    needs: test
[[- end]]
    strategy:
      matrix:
        include:[[range .Linux]]
          - arch: [[.Arch]]
            # snapcraft builds against Ubuntu 24.04 (core24).
            runner: [[if eq .Runner "ubuntu-latest"]]ubuntu-24.04[[else]][[.Runner]][[end]][[end]]
    runs-on: ${{ matrix.runner }}
    # The Snap Store can be slow; a failed snap doesn't hold back the
    # release (see publish).
    timeout-minutes: 120
    env:
      SNAPCRAFT_STORE_CREDENTIALS: ${{ secrets.SNAPCRAFT_STORE_CREDENTIALS }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - name: Install build dependencies
        run: |
          sudo apt-get update
          sudo apt-get install -y gcc pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libvulkan-dev
          go install github.com/xmhrnx/giode-cli/cmd/giode@latest
[[- if .SetupLinux]]
      - name: Project setup (giode.toml ci.setup)
        run: |
[[.SetupLinux]]
[[- end]]
      - name: Snap project
        # Without snapcraft here, giode writes the project the next step
        # packs.
        run: giode build -arch ${{ matrix.arch }} snap
      - name: Pack the snap
        id: snapcraft
        # On the runner itself (Ubuntu 24.04, as core24), without
        # snapcraft's build container.
        working-directory: dist/linux/snap
        run: |
          sudo snap install snapcraft --classic
          sudo snapcraft pack --destructive-mode
          echo "snap=$PWD/$(ls -1 *.snap | head -n 1)" >> "$GITHUB_OUTPUT"
      - uses: actions/upload-artifact@v4
        with:
          name: snap-${{ matrix.arch }}
          path: ${{ steps.snapcraft.outputs.snap }}
      - name: Upload to the Snap Store
        if: github.ref_type == 'tag' && env.SNAPCRAFT_STORE_CREDENTIALS != ''
        uses: snapcore/action-publish@v1
        with:
          snap: ${{ steps.snapcraft.outputs.snap }}
          release: [[.SnapChannel]]
[[- end]]
[[- if .MacOS]]

  macos:
[[- if .Test]]
    needs: test
[[- end]]
    runs-on: macos-latest
    # A hung signing step fails in an hour rather than GitHub's six.
    timeout-minutes: 60
    env:
      MACOS_CERT_BASE64: ${{ secrets.MACOS_CERT_BASE64 }}
[[- if .MAS]]
      MACOS_STORE_CERT_BASE64: ${{ secrets.MACOS_STORE_CERT_BASE64 }}
      MACOS_INSTALLER_CERT_BASE64: ${{ secrets.MACOS_INSTALLER_CERT_BASE64 }}
      MACOS_PROFILE_BASE64: ${{ secrets.MACOS_PROFILE_BASE64 }}
[[- end]]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go install github.com/xmhrnx/giode-cli/cmd/giode@latest
[[- if .SetupMac]]
      - name: Project setup (giode.toml ci.setup)
        run: |
[[.SetupMac]]
[[- end]]
      - name: Signing certificate
        if: env.MACOS_CERT_BASE64 != ''
        env:
          MACOS_CERT_PASS: ${{ secrets.MACOS_CERT_PASS }}
        run: |
          echo "$MACOS_CERT_BASE64" | base64 -d > cert.p12
          security create-keychain -p ci build.keychain
          security default-keychain -s build.keychain
          security unlock-keychain -p ci build.keychain
          security import cert.p12 -k build.keychain -P "$MACOS_CERT_PASS" -T /usr/bin/codesign
          security set-key-partition-list -S apple-tool:,apple: -s -k ci build.keychain
          rm cert.p12
      - name: Build
        env:
          # Signed with the certificate's Developer ID identity, and
          # notarized with these (macos.notary_id, macos.notary_team_id
          # when not set here).
          GIODE_NOTARY_ID: ${{ secrets.GIODE_NOTARY_ID }}
          GIODE_NOTARY_TEAM_ID: ${{ secrets.GIODE_NOTARY_TEAM_ID }}
          GIODE_NOTARY_PASS: ${{ secrets.GIODE_NOTARY_PASS }}
        run: |
          release=""
          if [ -n "$MACOS_CERT_BASE64" ]; then release="-release"; fi
          giode build $release dmg
[[- if .MAS]]
      - name: Mac App Store certificates and profile
        if: env.MACOS_STORE_CERT_BASE64 != '' && env.MACOS_INSTALLER_CERT_BASE64 != '' && env.MACOS_PROFILE_BASE64 != ''
        env:
          MACOS_STORE_CERT_PASS: ${{ secrets.MACOS_STORE_CERT_PASS }}
          MACOS_INSTALLER_CERT_PASS: ${{ secrets.MACOS_INSTALLER_CERT_PASS }}
        run: |
          if ! security list-keychains -d user | grep -q build.keychain; then
            security create-keychain -p ci build.keychain
            security default-keychain -s build.keychain
          fi
          security list-keychains -d user -s build.keychain $(security list-keychains -d user | tr -d '"')
          security unlock-keychain -p ci build.keychain
          echo "$MACOS_STORE_CERT_BASE64" | base64 -d > store.p12
          echo "$MACOS_INSTALLER_CERT_BASE64" | base64 -d > installer.p12
          security import store.p12 -k build.keychain -P "$MACOS_STORE_CERT_PASS" -T /usr/bin/codesign -T /usr/bin/productbuild
          security import installer.p12 -k build.keychain -P "$MACOS_INSTALLER_CERT_PASS" -T /usr/bin/codesign -T /usr/bin/productbuild
          security set-key-partition-list -S apple-tool:,apple: -s -k ci build.keychain
          rm store.p12 installer.p12
          echo "$MACOS_PROFILE_BASE64" | base64 -d > "$RUNNER_TEMP/store.provisionprofile"
          echo "GIODE_MACOS_STORE_PROFILE=$RUNNER_TEMP/store.provisionprofile" >> "$GITHUB_ENV"
      - name: Build for the Mac App Store
        if: env.GIODE_MACOS_STORE_PROFILE != ''
        run: giode build mas
[[- end]]
      - uses: actions/upload-artifact@v4
        with:
          name: macos
          if-no-files-found: ignore
          path: |
            dist/macos/*.dmg
            dist/macos/appstore/*.pkg
[[- end]]
[[- if .IOS]]

  ios:
[[- if .Test]]
    needs: test
[[- end]]
    runs-on: macos-latest
    # A hung signing step fails in an hour rather than GitHub's six.
    timeout-minutes: 60
    env:
      IOS_CERT_BASE64: ${{ secrets.IOS_CERT_BASE64 }}
      IOS_PROFILE_BASE64: ${{ secrets.IOS_PROFILE_BASE64 }}
[[- if .ShareTarget]]
      IOS_SHARE_PROFILE_BASE64: ${{ secrets.IOS_SHARE_PROFILE_BASE64 }}
[[- end]]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go install github.com/xmhrnx/giode-cli/cmd/giode@latest
[[- if .SetupMac]]
      - name: Project setup (giode.toml ci.setup)
        run: |
[[.SetupMac]]
[[- end]]
      - name: Signing certificate and profile
        if: env.IOS_CERT_BASE64 != '' && env.IOS_PROFILE_BASE64 != ''
        env:
          IOS_CERT_PASS: ${{ secrets.IOS_CERT_PASS }}
        run: |
          echo "$IOS_CERT_BASE64" | base64 -d > cert.p12
          security create-keychain -p ci build.keychain
          security default-keychain -s build.keychain
          security unlock-keychain -p ci build.keychain
          security import cert.p12 -k build.keychain -P "$IOS_CERT_PASS" -T /usr/bin/codesign
          security set-key-partition-list -S apple-tool:,apple: -s -k ci build.keychain
          rm cert.p12
          echo "$IOS_PROFILE_BASE64" | base64 -d > "$RUNNER_TEMP/app.mobileprovision"
          echo "GIODE_IOS_PROFILE=$RUNNER_TEMP/app.mobileprovision" >> "$GITHUB_ENV"
[[- if .ShareTarget]]
          # app.share_target: the share extension has its own profile.
          if [ -n "$IOS_SHARE_PROFILE_BASE64" ]; then
            echo "$IOS_SHARE_PROFILE_BASE64" | base64 -d > "$RUNNER_TEMP/share.mobileprovision"
            echo "GIODE_IOS_SHARE_PROFILE=$RUNNER_TEMP/share.mobileprovision" >> "$GITHUB_ENV"
          fi
[[- end]]
      - name: Build
        env:
          GIODE_IOS_TEAM_ID: ${{ secrets.IOS_TEAM_ID }}
        run: |
[[- if .ShareTarget]]
          # A device build needs the certificate and both profiles, the
          # app's and the share extension's; without them, check that
          # the app builds for the simulator.
          if [ -n "$GIODE_IOS_PROFILE" ] && [ -n "$GIODE_IOS_SHARE_PROFILE" ]; then
            giode build ios
          else
            if [ -n "$GIODE_IOS_PROFILE" ]; then
              echo "::warning::app.share_target needs the IOS_SHARE_PROFILE_BASE64 secret for a device build; building for the simulator"
            fi
            giode build ios-sim
          fi
[[- else]]
          # A device build needs the certificate and profile; without
          # them, check that the app builds for the simulator.
          if [ -n "$GIODE_IOS_PROFILE" ]; then giode build ios; else giode build ios-sim; fi
[[- end]]
      - uses: actions/upload-artifact@v4
        with:
          name: ios
          if-no-files-found: ignore
          path: dist/ios/*.ipa
[[- end]]
[[- if .WindowsMSYS2]]

  windows:
[[- if .Test]]
    needs: test
[[- end]]
    strategy:
      matrix:
        include:[[range .Windows]]
          - arch: [[.Arch]]
            runner: [[.Runner]]
            msystem: [[.MSYSTEM]]
            cc: [[.CC]]
            choco_nsis: [[.ChocoNSIS]][[end]]
    runs-on: ${{ matrix.runner }}
    defaults:
      run:
        shell: msys2 {0}
    env:
      CC: ${{ matrix.cc }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - uses: msys2/setup-msys2@v2
        with:
          msystem: ${{ matrix.msystem }}
          path-type: inherit
          pacboy: >-
            toolchain:p
            pkgconf:p
      - name: NSIS
        shell: pwsh
        run: |
          if ("${{ matrix.choco_nsis }}" -eq "true") { choco install nsis -y; "C:\Program Files (x86)\NSIS" | Out-File -Append $env:GITHUB_PATH }
          else { C:\msys64\usr\bin\pacman.exe -S --noconfirm mingw-w64-x86_64-nsis }
      - run: go install github.com/xmhrnx/giode-cli/cmd/giode@latest
[[- if .SetupWin]]
      - name: Project setup (giode.toml ci.setup; $MINGW_PACKAGE_PREFIX names this runner's packages)
        run: |
[[.SetupWin]]
[[- end]]
      - name: Signing certificate
        env:
          WINDOWS_CERT_BASE64: ${{ secrets.WINDOWS_CERT_BASE64 }}
        run: |
          if [ -n "$WINDOWS_CERT_BASE64" ]; then echo "$WINDOWS_CERT_BASE64" | base64 -d > [[.Cert]]; fi
      - name: Build
        env:
          GIODE_WINDOWS_CERT_PASS: ${{ secrets.GIODE_WINDOWS_CERT_PASS }}
        run: |
          win=""; if [ -n "$GIODE_WINDOWS_CERT_PASS" ]; then win="-release"; fi
          "$(go env GOPATH)/bin/giode" build $win -arch ${{ matrix.arch }} windows-installer msix
      - uses: actions/upload-artifact@v4
        with:
          name: windows-${{ matrix.arch }}
          path: |
            dist/windows/*.exe
            dist/windows/*.msix
[[- end]]

  publish:
    needs: [[printf "[%s]" .Needs]]
    if: [[.PublishIf]]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@v4
        with:
          path: artifacts
          merge-multiple: true
      - uses: softprops/action-gh-release@v2
        with:
          files: artifacts/**/*
[[- if .Release]]
          tag_name: ${{ github.ref_name }}
[[- else]]
          generate_release_notes: true
[[- end]]
`
