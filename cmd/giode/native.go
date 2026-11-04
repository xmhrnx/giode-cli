package main

import (
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/xmhrnx/giode-cli/internal/config"
	"github.com/xmhrnx/giode-cli/internal/native"
	"github.com/xmhrnx/giode-cli/internal/progress"
)

// nativeLibs returns the shared libraries bin loads that [native]
// bundles, warning about imported ones it couldn't find.
func nativeLibs(c *config.Config, goos, bin string) ([]native.Lib, error) {
	// Gio's OpenGL renderer on Windows is ANGLE, which must ship with
	// the app: bundle it (and what it needs) whether or not [native] is.
	angle := goos == "windows" && c.App.Renderer == "opengl"
	if !c.Native.Bundle && !angle {
		return nil, nil
	}
	search, libs, exclude, err := c.NativeOptions(goos)
	if err != nil {
		return nil, err
	}
	if angle && !hasLib(libs, "libEGL.dll") {
		found, err := findANGLE(bin, search)
		if err != nil {
			return nil, err
		}
		if verbose {
			progress.Logf("ANGLE for app.renderer = \"opengl\": %s", filepath.Dir(found[0]))
		}
		libs = append(libs, found...)
		// Its own dependencies (MinGW's runtime) are usually next to it.
		search = append(search, filepath.Dir(found[0]))
	}
	r, err := native.Collect(bin, native.Options{GOOS: goos, Search: search, Libraries: libs, Exclude: exclude})
	if err != nil {
		return nil, fmt.Errorf("native libraries: %w", err)
	}
	if len(r.Missing) > 0 {
		section := map[string]string{"windows": "native.windows", "darwin": "native.macos"}[goos]
		if section == "" {
			section = "native.linux"
		}
		progress.Logf("warning: %s needs %s, not found in %s.search; the package won't run where they're missing",
			filepath.Base(bin), strings.Join(r.Missing, ", "), section)
	}
	if verbose {
		for _, l := range r.Libs {
			progress.Logf("bundling %s (%s)", l.Name, l.Path)
		}
	}
	return r.Libs, nil
}

func hasLib(libs []string, name string) bool {
	for _, l := range libs {
		if strings.EqualFold(filepath.Base(l), name) {
			return true
		}
	}
	return false
}

// angleDirs are where ANGLE (libEGL.dll and libGLESv2.dll) is found on
// a build machine: MSYS2's angleproject package, then a VS Code install
// (older versions ship it as DLLs; Chrome, Edge and newer Electron
// apps link it into their own binaries).
func angleDirs() []string {
	var dirs []string
	if p := os.Getenv("MSYSTEM_PREFIX"); p != "" {
		dirs = append(dirs, filepath.Join(p, "bin"))
	}
	for _, root := range []string{os.Getenv("MSYS2_ROOT"), `C:\msys64`, "/c/msys64"} {
		if root == "" {
			continue
		}
		for _, env := range []string{"clangarm64", "clang64", "ucrt64", "mingw64"} {
			dirs = append(dirs, filepath.Join(root, env, "bin"))
		}
	}
	for _, p := range []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Microsoft VS Code"), `C:\Program Files\Microsoft VS Code`} {
		if !strings.HasPrefix(p, "Programs") {
			dirs = append(dirs, p)
			dirs = append(dirs, newestFirst(p)...)
		}
	}
	return dirs
}

// newestFirst returns dir's subfolders, most recently changed first:
// VS Code keeps its DLLs in a folder named after its build, and an
// update leaves the old one beside the new.
func newestFirst(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type sub struct {
		path string
		mod  time.Time
	}
	var subs []sub
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			subs = append(subs, sub{filepath.Join(dir, e.Name()), info.ModTime()})
		}
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].mod.After(subs[j].mod) })
	out := make([]string, len(subs))
	for i, s := range subs {
		out[i] = s.path
	}
	return out
}

// findANGLE finds libEGL.dll and libGLESv2.dll for the executable's CPU,
// in the search directories and then where ANGLE is usually installed.
func findANGLE(exe string, search []string) ([]string, error) {
	want := peMachine(exe)
	for _, d := range append(append([]string{}, search...), angleDirs()...) {
		egl, gles := filepath.Join(d, "libEGL.dll"), filepath.Join(d, "libGLESv2.dll")
		if _, err := os.Stat(egl); err != nil {
			continue
		}
		if _, err := os.Stat(gles); err != nil {
			continue
		}
		if want != 0 && peMachine(egl) != want {
			continue
		}
		return []string{egl, gles}, nil
	}
	return nil, errors.New(`app.renderer = "opengl" needs ANGLE (libEGL.dll and libGLESv2.dll) on Windows, and none was found for this CPU: install MSYS2's angleproject (pacman -S mingw-w64-x86_64-angleproject, or mingw-w64-clang-aarch64-angleproject for ARM64), or list the DLLs in [native.windows] libraries`)
}

// peMachine returns a PE file's CPU, or 0.
func peMachine(file string) uint16 {
	f, err := pe.Open(file)
	if err != nil {
		return 0
	}
	defer f.Close()
	return f.FileHeader.Machine
}

// copyLibs copies libs into dir and returns their new paths.
func copyLibs(libs []native.Lib, dir string) ([]string, error) {
	if len(libs) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var out []string
	for _, l := range libs {
		dst := filepath.Join(dir, l.Name)
		if err := copyLib(l.Path, dst); err != nil {
			return nil, err
		}
		out = append(out, dst)
	}
	return out, nil
}

// copyLib copies src (following links) to dst, writable. A file isn't
// copied onto itself.
func copyLib(src, dst string) error {
	if si, err := os.Stat(src); err == nil {
		if di, err := os.Stat(dst); err == nil && os.SameFile(si, di) {
			return nil
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	os.Remove(dst)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// windowsDLLs bundles the DLLs exe loads next to it and returns their
// paths.
func windowsDLLs(c *config.Config, exe string) ([]string, error) {
	libs, err := nativeLibs(c, "windows", exe)
	if err != nil {
		return nil, err
	}
	return copyLibs(libs, filepath.Dir(exe))
}

// linuxRpathLdflag is the linker flag giving a Linux binary the run
// path of its bundled libraries: lib next to it (dist/linux), and its
// package's private lib directory. It's a DT_RPATH, not the DT_RUNPATH
// linkers write by default, which applies only to the binary's own
// imports and not to the libraries those load.
func linuxRpathLdflag(c *config.Config, command string) string {
	if !c.Native.Bundle {
		return ""
	}
	return "-extldflags=-Wl,--disable-new-dtags,-rpath,$ORIGIN/lib:$ORIGIN/../lib/" + command
}

// macBundleLibs copies the libraries a macOS app's executable loads
// into Contents/Frameworks, as dylibbundler does: each renamed
// @rpath/<name>, every reference to it rewritten, the executable given
// the Frameworks run path, and the changed files signed ad hoc (the
// app's own signing, when there is one, signs them again).
func macBundleLibs(c *config.Config) func(app string) error {
	if !c.Native.Bundle {
		return nil
	}
	return func(app string) error {
		bins, _ := filepath.Glob(filepath.Join(app, "Contents", "MacOS", "*"))
		if len(bins) == 0 {
			return fmt.Errorf("no executable in %s", app)
		}
		bin := bins[0]
		libs, err := nativeLibs(c, "darwin", bin)
		if err != nil || len(libs) == 0 {
			return err
		}
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("bundling macOS libraries needs install_name_tool, on macOS")
		}
		fw := filepath.Join(app, "Contents", "Frameworks")
		copied, err := copyLibs(libs, fw)
		if err != nil {
			return err
		}
		plan := native.PlanMac(libs)
		tool := func(args ...string) error {
			cmd := exec.Command("install_name_tool", args...)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("install_name_tool %s: %v\n%s", strings.Join(args, " "), err, out)
			}
			return nil
		}
		for i, l := range libs {
			if err := tool(plan.InstallNameArgs(l.Name, copied[i])...); err != nil {
				return err
			}
		}
		if err := tool(plan.InstallNameArgs("", bin)...); err != nil {
			return err
		}
		// Already there when the binary was built with it: ignore.
		exec.Command("install_name_tool", "-add_rpath", "@executable_path/../Frameworks", bin).Run()
		for _, f := range append(copied, bin) {
			if out, err := exec.Command("codesign", "--force", "--sign", "-", f).CombinedOutput(); err != nil {
				return fmt.Errorf("codesign %s: %v\n%s", filepath.Base(f), err, out)
			}
		}
		return nil
	}
}

// checkNativeTools checks the tools [native] bundling needs here.
func checkNativeTools(c *config.Config) []check {
	if !c.Native.Bundle || runtime.GOOS != "darwin" {
		return nil
	}
	var checks []check
	for _, t := range []string{"install_name_tool", "codesign"} {
		ch := check{area: "Native libraries (giode.toml [native])", name: t}
		if p, err := exec.LookPath(t); err == nil {
			ch.ok, ch.detail = true, p
		} else {
			ch.fix = "Install Xcode's command line tools: xcode-select --install"
		}
		checks = append(checks, ch)
	}
	return checks
}
