package main

import (
	"github.com/mehran9675/giode-cli/internal/config"
	"github.com/mehran9675/giode-cli/internal/gogio"

	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const doctorUsage = `
Usage: giode doctor

Checks the tools each target needs and says how to install what's
missing: Go for every target; the Android SDK, build tools, platform,
NDK and a JDK for Android; Xcode for iOS and macOS; the Wayland, X11
and Vulkan development libraries for Linux.
`

// check is one doctor finding.
type check struct {
	area, name string
	ok         bool
	detail     string
	fix        string
}

func cmdDoctor(args []string) error {
	fs := flags("doctor", doctorUsage)
	if _, err := parse(fs, args); err != nil {
		return err
	}
	var checks []check
	checks = append(checks, checkGo())
	checks = append(checks, checkAndroid()...)
	checks = append(checks, checkWindowsPackaging()...)
	if c, err := loadProject("."); err == nil {
		checks = append(checks, checkProject(c)...)
	}
	switch runtime.GOOS {
	case "darwin":
		checks = append(checks, checkXcode()...)
	case "linux":
		checks = append(checks, checkLinuxLibs())
	}
	area, missing := "", 0
	for _, c := range checks {
		if c.area != area {
			area = c.area
			fmt.Printf("\n%s\n", area)
		}
		mark := "✓"
		if !c.ok {
			mark = "✗"
			missing++
		}
		fmt.Printf("  %s %s", mark, c.name)
		if c.detail != "" {
			fmt.Printf(": %s", c.detail)
		}
		fmt.Println()
		if !c.ok && c.fix != "" {
			for _, l := range strings.Split(c.fix, "\n") {
				fmt.Printf("      %s\n", l)
			}
		}
	}
	fmt.Println()
	if missing > 0 {
		fmt.Printf("%d problem(s) found. Targets that don't need the missing tools still build.\n", missing)
	} else {
		fmt.Println("Everything giode needs on this computer is installed.")
	}
	return nil
}

func checkGo() check {
	c := check{area: "Go (every target)", name: "go"}
	v, err := output("go", "env", "GOVERSION")
	if err != nil {
		c.fix = "Install Go 1.26 or later from https://go.dev/dl"
		return c
	}
	c.detail = v
	maj, min := goVersion(v)
	c.ok = maj > 1 || maj == 1 && min >= 26
	if !c.ok {
		c.fix = "giode needs Go 1.26 or later: https://go.dev/dl"
	}
	return c
}

// goVersion parses "go1.26.6".
func goVersion(v string) (major, minor int) {
	parts := strings.Split(strings.TrimPrefix(v, "go"), ".")
	if len(parts) < 2 {
		return 0, 0
	}
	major, _ = strconv.Atoi(parts[0])
	// The leading digits: "27rc1" is 27.
	n := strings.IndexFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' })
	if n < 0 {
		n = len(parts[1])
	}
	minor, _ = strconv.Atoi(parts[1][:n])
	return major, minor
}

func checkAndroid() []check {
	const area = "Android (giode build android, giode run android)"
	sdkFix := "Install Android Studio (https://developer.android.com/studio), or the command-line tools,\n" +
		"then set ANDROID_HOME to the SDK directory."
	home := androidHome()
	sdk := check{area: area, name: "Android SDK", ok: home != "", detail: home, fix: sdkFix}
	if home == "" {
		sdk.detail = "ANDROID_HOME isn't set and no SDK is in the default location"
		return []check{sdk, checkJDK(area)}
	}
	latest := func(pattern string) string {
		m, _ := filepath.Glob(filepath.Join(home, pattern))
		sort.Strings(m)
		if len(m) == 0 {
			return ""
		}
		return m[len(m)-1]
	}
	sdkmanager := "sdkmanager"
	if p := latest("cmdline-tools/*/bin/sdkmanager"); p != "" {
		sdkmanager = p
	}
	bt := latest("build-tools/*")
	plat := latest("platforms/android-*")
	ndk := latest("ndk/*")
	if ndk == "" {
		ndk = latest("ndk-bundle")
	}
	pt := filepath.Join(home, "platform-tools", "adb"+exe())
	_, ptErr := os.Stat(pt)
	return []check{
		sdk,
		{area: area, name: "build tools", ok: bt != "", detail: filepath.Base(bt), fix: sdkmanager + ` "build-tools;35.0.0"`},
		{area: area, name: "platform", ok: plat != "", detail: filepath.Base(plat), fix: sdkmanager + ` "platforms;android-35"`},
		{area: area, name: "NDK", ok: ndk != "", detail: filepath.Base(ndk), fix: sdkmanager + ` "ndk;27.2.12479018"`},
		{area: area, name: "platform tools (adb)", ok: ptErr == nil, fix: sdkmanager + ` "platform-tools"`},
		checkBundletool(area, bt),
		checkJDK(area),
	}
}

// checkBundletool reports where app bundles (giode build aab) get
// bundletool: the build tools folder, giode's cache, or a download on
// the first bundle build, which needs the network.
func checkBundletool(area, buildtools string) check {
	c := check{area: area, name: "bundletool (giode build aab)", ok: true}
	if found, _ := filepath.Glob(filepath.Join(buildtools, "bundletool*.jar")); len(found) > 0 {
		c.detail = filepath.Base(found[0])
		return c
	}
	if p, err := gogio.BundletoolCachePath(); err == nil {
		if _, err := os.Stat(p); err == nil {
			c.detail = p
			return c
		}
	}
	c.detail = "downloaded from github.com/google/bundletool on the first app bundle build"
	return c
}

func checkJDK(area string) check {
	c := check{area: area, name: "JDK (javac, keytool)", fix: "Install a JDK 17 or later (https://adoptium.net) and put it on PATH or set JAVA_HOME."}
	javac := "javac"
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		javac = filepath.Join(jh, "bin", "javac"+exe())
	}
	if _, err := exec.LookPath(javac); err != nil {
		return c
	}
	cmd := exec.Command(javac, "-version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return c
	}
	c.ok = true
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	c.detail = lines[len(lines)-1]
	return c
}

func checkXcode() []check {
	const area = "Apple (giode build ios, ios-sim, macos)"
	c := check{area: area, name: "Xcode", fix: "Install Xcode from the App Store, then run: sudo xcode-select -s /Applications/Xcode.app"}
	if v, err := output("xcodebuild", "-version"); err == nil {
		c.ok = true
		c.detail = strings.Split(v, "\n")[0]
	}
	sim := check{area: area, name: "iOS simulator", fix: "Install an iOS simulator runtime in Xcode > Settings > Components."}
	if out, err := output("xcrun", "simctl", "list", "runtimes"); err == nil && strings.Contains(out, "iOS") {
		sim.ok = true
	}
	return []check{c, sim}
}

func checkLinuxLibs() check {
	c := check{area: "Linux (giode build linux, giode run)", name: "Gio's system libraries"}
	// The packages Gio's cgo code asks pkg-config for.
	pkgs := []string{"wayland-client", "wayland-cursor", "wayland-egl", "x11", "x11-xcb", "xcursor", "xfixes", "xkbcommon", "xkbcommon-x11", "egl"}
	if _, err := exec.LookPath("pkg-config"); err != nil {
		c.detail = "pkg-config isn't installed, so the libraries can't be checked"
		c.fix = "Install pkg-config, then run giode doctor again."
		return c
	}
	var missing []string
	for _, p := range pkgs {
		if err := exec.Command("pkg-config", "--exists", p).Run(); err != nil {
			missing = append(missing, p)
		}
	}
	c.ok = len(missing) == 0
	if !c.ok {
		c.detail = "missing " + strings.Join(missing, ", ")
		c.fix = "Debian/Ubuntu: sudo apt install gcc pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev\n" +
			"  libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libxfixes-dev libvulkan-dev\n" +
			"Fedora: sudo dnf install gcc pkg-config wayland-devel libX11-devel libxkbcommon-x11-devel mesa-libGLES-devel\n" +
			"  mesa-libEGL-devel libXcursor-devel libXfixes-devel vulkan-headers\n" +
			"Others: https://gioui.org/doc/install/linux"
	}
	return c
}

func checkWindowsPackaging() []check {
	const area = "Windows packaging (giode build windows-installer, -release signing)"
	nsis := check{area: area, name: "NSIS (makensis)", fix: "winget install NSIS.NSIS, brew install makensis, or apt install nsis"}
	if p, err := exec.LookPath("makensis"); err == nil {
		nsis.ok, nsis.detail = true, p
	} else if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
			if p := filepath.Join(root, "NSIS", "makensis.exe"); root != "" {
				if _, err := os.Stat(p); err == nil {
					nsis.ok, nsis.detail = true, p
				}
			}
		}
	}
	signer := check{area: area, name: "osslsigncode", fix: "apt install osslsigncode, or brew install osslsigncode"}
	tool := "osslsigncode"
	if runtime.GOOS == "windows" {
		signer = check{area: area, name: "signtool", fix: "Install the Windows SDK's \"Signing Tools for Desktop Apps\""}
		tool = "signtool"
	}
	if p, err := exec.LookPath(tool); err == nil {
		signer.ok, signer.detail = true, p
	} else if runtime.GOOS == "windows" {
		m, _ := filepath.Glob(filepath.Join(os.Getenv("ProgramFiles(x86)"), "Windows Kits", "10", "bin", "*", "x64", "signtool.exe"))
		if len(m) > 0 {
			signer.ok, signer.detail = true, m[len(m)-1]
		}
	}
	return []check{nsis, signer}
}

// checkProject checks the tools the project in the current directory
// asks for in giode.toml.
func checkProject(c *config.Config) []check {
	var checks []check
	if w := c.Windows; w.CC != "" {
		area := "Windows cgo (giode.toml windows.cc)"
		for _, tool := range []struct{ name, fix string }{
			{w.CC, "Install a MinGW-w64 C compiler: apt install gcc-mingw-w64-x86-64, brew install mingw-w64, or MSYS2's mingw-w64-x86_64-gcc on Windows"},
			{w.CXX, "Install the MinGW-w64 C++ compiler (g++-mingw-w64-x86-64)"},
			{firstNonEmpty(w.PkgConfig, "pkg-config"), "Install pkg-config (apt install pkg-config, brew install pkg-config, MSYS2's mingw-w64-x86_64-pkgconf)"},
		} {
			if tool.name == "" {
				continue
			}
			ch := check{area: area, name: tool.name}
			if p, err := exec.LookPath(tool.name); err == nil {
				ch.ok, ch.detail = true, p
			} else {
				ch.fix = tool.fix
			}
			checks = append(checks, ch)
		}
		if w.PkgConfigPath != "" {
			ch := check{area: area, name: "pkg_config_path"}
			missing := []string{}
			for _, d := range filepath.SplitList(w.PkgConfigPath) {
				if _, err := os.Stat(c.Path(d)); err != nil {
					missing = append(missing, d)
				}
			}
			ch.ok = len(missing) == 0
			ch.detail = w.PkgConfigPath
			if !ch.ok {
				ch.fix = "Not found: " + strings.Join(missing, ", ") + " - the directory with the Windows libraries' .pc files"
			}
			checks = append(checks, ch)
		}
	}
	checks = append(checks, checkNativeTools(c)...)
	return checks
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
