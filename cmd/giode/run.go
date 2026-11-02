package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/xmhrnx/giode-cli/internal/config"
)

const runUsage = `
Usage: giode run [options] [target] [-- app arguments]

Builds the app in giode.toml for a target and runs it, like giode build
followed by launching the result:

	desktop   this computer, straight from source with go run (default)
	linux     the Linux executable (on Linux)
	windows   the Windows executable: directly on Windows, with Wine elsewhere
	windows-installer
	          builds the setup program and runs it (Wine outside Windows)
	macos     the macOS app bundle (on macOS)
	android   a connected device or emulator: builds for its architecture,
	          installs, launches and follows the app's log
	ios-sim   the booted iOS simulator, showing the app's output (macOS)
	web       serves dist/web and prints the address to open

Short names work too: win, mac, sim, apk, browser, installer.

Example:

	giode run android
	giode run mac
	giode run -mcp
`

func cmdRun(args []string) error {
	fs := flags("run", runUsage)
	dir := fs.String("C", ".", "project directory")
	device := fs.String("device", "", "Android device serial or simulator UDID (giode devices)")
	mcp := fs.Bool("mcp", false, "start giode's MCP server so an AI agent can drive the app (GIODE_MCP=1)")
	release := fs.Bool("release", false, "run a release build, signed as giode build -release signs it")
	fs.BoolVar(&verbose, "x", false, "print the commands run")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	target, appArgs := "desktop", []string(nil)
	if len(pos) > 0 {
		target, appArgs = canonical(pos[0]), pos[1:]
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if *mcp {
		os.Setenv("GIODE_MCP", "1")
	}
	o := buildOpts{release: *release, args: appArgs}
	switch target {
	case "desktop":
		gb, err := prepareGio(c, "desktop")
		if err != nil {
			return err
		}
		defer gb.cleanup()
		// go build, then run: what go run does, with the build timed.
		tmp, err := os.MkdirTemp("", "giode-run-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		bin := filepath.Join(tmp, binaryName(c))
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		fmt.Println("Building…")
		start := time.Now()
		args := append(append([]string{"build", "-o", bin, "-ldflags", desktopLdflags(c, hostTarget())}, gb.goArgs()...), pkgPath(c))
		if err := run(command(c, "go", args...)); err != nil {
			return err
		}
		fmt.Printf("Built in %s. Running %s\n", since(start), c.App.Name)
		return run(command(c, bin, appArgs...))
	case "linux":
		if runtime.GOOS != "linux" {
			return errors.New("the Linux app runs on Linux; use giode run here, or run it in a Linux VM")
		}
		bin, err := built(c, target, o)
		if err != nil {
			return err
		}
		return run(command(c, bin, appArgs...))
	case "windows", "windows-installer":
		exe, err := built(c, target, o)
		if err != nil {
			return err
		}
		if runtime.GOOS == "windows" {
			return run(command(c, exe, appArgs...))
		}
		wine, err := findWine()
		if err != nil {
			return fmt.Errorf("built %s; running it here needs Wine: %w", relPath(exe), err)
		}
		return run(command(c, wine, append([]string{exe}, appArgs...)...))
	case "macos":
		if runtime.GOOS != "darwin" {
			return errors.New("the macOS app runs on macOS")
		}
		app, err := built(c, target, o)
		if err != nil {
			return err
		}
		// Run the executable inside the bundle, so its output shows here.
		bins, _ := filepath.Glob(filepath.Join(app, "Contents", "MacOS", "*"))
		if len(bins) == 0 {
			return run(command(c, "open", "-W", "-n", app))
		}
		return run(command(c, bins[0], appArgs...))
	case "android":
		return runAndroid(c, *device, o)
	case "ios-sim":
		return runIOSSim(c, *device, appArgs)
	case "ios":
		return errors.New("giode runs iOS apps in the simulator (giode run ios-sim); install device builds with Xcode's Devices window or Apple Configurator")
	case "web":
		return runWeb(c)
	}
	return fmt.Errorf("unknown target %q; targets: desktop, %s", target, strings.Join(targetNames, ", "))
}

// built builds target, printing what it's doing.
func built(c *config.Config, target string, o buildOpts) (string, error) {
	fmt.Printf("Building %s…\n", target)
	start := time.Now()
	out, err := buildTarget(c, target, o)
	if err != nil {
		return "", err
	}
	fmt.Printf("Built in %s. Running %s\n", since(start), relPath(out))
	return out, nil
}

// findWine finds Wine, to run Windows programs on Linux and macOS.
func findWine() (string, error) {
	for _, w := range []string{"wine", "wine64"} {
		if p, err := exec.LookPath(w); err == nil {
			return p, nil
		}
	}
	return "", errors.New("install it (apt install wine, brew install --cask wine-stable)")
}

// abiArch maps Android ABIs to GOARCHs.
var abiArch = map[string]string{
	"arm64-v8a":   "arm64",
	"armeabi-v7a": "arm",
	"x86_64":      "amd64",
	"x86":         "386",
}

func runAndroid(c *config.Config, device string, o buildOpts) error {
	adbPath, err := adb()
	if err != nil {
		return err
	}
	if device != "" {
		os.Setenv("ANDROID_SERIAL", device)
	}
	if device == "" {
		devs, err := androidDevices(adbPath)
		if err != nil {
			return err
		}
		switch len(devs) {
		case 0:
			return errors.New("no Android device is connected: plug one in with USB debugging on, or start an emulator")
		case 1:
		default:
			return fmt.Errorf("several Android devices are connected; pick one with -device (%s)", strings.Join(devs, ", "))
		}
	}
	// Build only the device's architecture: a quarter of the work.
	abi, err := output(adbPath, "shell", "getprop", "ro.product.cpu.abi")
	if err != nil {
		return err
	}
	o.arch = abiArch[abi]
	if o.arch == "" {
		return fmt.Errorf("unknown device ABI %q", abi)
	}
	fmt.Printf("Building for %s (%s)…\n", abi, o.arch)
	start := time.Now()
	apk, err := buildTarget(c, "android", o)
	if err != nil {
		return err
	}
	fmt.Printf("Built in %s. Installing…\n", since(start))
	installStart := time.Now()
	if err := run(command(c, adbPath, "install", "-r", "-d", apk)); err != nil {
		return err
	}
	fmt.Printf("Installed in %s.\n", since(installStart))
	// Clear old log lines, start the app and follow its log.
	run(command(c, adbPath, "logcat", "-c"))
	if err := run(command(c, adbPath, "shell", "monkey", "-p", c.App.ID, "-c", "android.intent.category.LAUNCHER", "1")); err != nil {
		return err
	}
	var pid string
	for i := 0; i < 50 && pid == ""; i++ {
		pid, _ = output(adbPath, "shell", "pidof", c.App.ID)
		if pid == "" {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if pid == "" {
		return errors.New("the app didn't start; check adb logcat for a crash")
	}
	fmt.Printf("Running %s (pid %s). Press Ctrl+C to stop following the log.\n\n", c.App.ID, pid)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd := exec.CommandContext(ctx, adbPath, "logcat", "-v", "brief", "--pid="+strings.Fields(pid)[0])
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// androidDevices lists the serials of connected, authorized devices.
func androidDevices(adbPath string) ([]string, error) {
	out, err := output(adbPath, "devices")
	if err != nil {
		return nil, err
	}
	var devs []string
	for _, line := range strings.Split(out, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == "device" {
			devs = append(devs, f[0])
		}
	}
	return devs, nil
}

func runIOSSim(c *config.Config, udid string, appArgs []string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("the iOS simulator needs macOS and Xcode")
	}
	if udid == "" {
		udid = "booted"
		if out, _ := output("xcrun", "simctl", "list", "devices", "booted"); !strings.Contains(out, "Booted") {
			return errors.New("no simulator is booted: open Simulator.app, or run xcrun simctl boot <device>")
		}
	}
	fmt.Println("Building ios-sim…")
	start := time.Now()
	app, err := buildTarget(c, "ios-sim", buildOpts{})
	if err != nil {
		return err
	}
	fmt.Printf("Built in %s. Installing…\n", since(start))
	installStart := time.Now()
	if err := run(command(c, "xcrun", "simctl", "install", udid, app)); err != nil {
		return err
	}
	fmt.Printf("Installed in %s.\n", since(installStart))
	args := append([]string{"simctl", "launch", "--console-pty", "--terminate-running-process", udid, c.App.ID}, appArgs...)
	return run(command(c, "xcrun", args...))
}

func runWeb(c *config.Config) error {
	fmt.Println("Building web…")
	start := time.Now()
	d, err := buildTarget(c, "web", buildOpts{})
	if err != nil {
		return err
	}
	fmt.Printf("Built in %s.\n", since(start))
	addr := "127.0.0.1:" + strconv.Itoa(c.Web.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("serving on %s: %w (set web.port in giode.toml)", addr, err)
	}
	fmt.Printf("Serving %s at http://%s/ - press Ctrl+C to stop.\n", relPath(d), addr)
	srv := &http.Server{Handler: noCache(http.FileServer(http.Dir(filepath.Clean(d))))}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// noCache makes browsers fetch a rebuilt main.wasm.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}
