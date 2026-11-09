package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/xmhrnx/giode-cli/internal/config"
	"github.com/xmhrnx/giode-cli/internal/linuxpkg"
	"github.com/xmhrnx/giode-cli/internal/msix"
	"github.com/xmhrnx/giode-cli/internal/progress"
	"github.com/xmhrnx/giode-cli/internal/winget"
)

// linuxApp describes the built Linux binary bin for packaging.
func linuxApp(c *config.Config, bin, arch string) linuxpkg.App {
	if arch == "" {
		arch = runtime.GOARCH
	}
	a := linuxpkg.App{
		Name: c.App.Name, ID: c.LinuxID(), Version: c.App.Version, Build: c.App.Build,
		Summary: c.App.Description, Description: c.Linux.Description,
		Publisher: c.App.Publisher, Maintainer: c.Linux.Maintainer,
		Website: c.App.Website, License: c.Linux.License, Categories: c.Linux.Categories,
		Binary: bin, Command: filepath.Base(bin), Icon: c.Path(c.App.Icon), Arch: arch,
		FlatpakPermissions: c.Linux.FlatpakPermissions, Schemes: c.App.Schemes,
	}
	for _, s := range c.Linux.Screenshots {
		a.Screenshots = append(a.Screenshots, linuxpkg.Screenshot{URL: s.URL, Caption: s.Caption})
	}
	a.MIMETypes, a.MIMEXML = c.LinuxMIMETypes(a.Command)
	return a
}

// buildLinuxPackage builds the Linux executable and packages it as a
// .deb, .rpm, AppImage or Flatpak.
func buildLinuxPackage(c *config.Config, kind string, o buildOpts) (string, error) {
	binOpts := o
	binOpts.output = ""
	bin, err := buildTarget(c, "linux", binOpts)
	if err != nil {
		return "", err
	}
	a := linuxApp(c, bin, o.arch)
	libs, err := nativeLibs(c, "linux", bin)
	if err != nil {
		return "", err
	}
	if c.Native.Linux.System && (kind == "deb" || kind == "rpm") {
		// The system's packages provide them.
		a.Depends = c.Native.Linux.DebDepends
		a.Requires = append([]string{}, c.Native.Linux.RPMRequires...)
		suffix := ""
		if strings.HasSuffix(linuxpkg.RPMArch(a.Arch), "64") || linuxpkg.RPMArch(a.Arch) == "ppc64le" {
			suffix = "(64bit)"
		}
		for _, l := range libs {
			a.Requires = append(a.Requires, l.Name+"()"+suffix)
		}
	} else {
		for _, l := range libs {
			a.Libs = append(a.Libs, l.Path)
		}
	}
	dir := filepath.Dir(bin)
	name := strings.ToLower(binaryName(c))
	var out string
	switch kind {
	case "deb":
		out = filepath.Join(dir, fmt.Sprintf("%s_%s-%d_%s.deb", name, c.App.Version, max(c.App.Build, 1), debArch(a.Arch)))
	case "rpm":
		out = filepath.Join(dir, fmt.Sprintf("%s-%s-%d.%s.rpm", name, c.App.Version, max(c.App.Build, 1), linuxpkg.RPMArch(a.Arch)))
	case "appimage":
		out = filepath.Join(dir, fmt.Sprintf("%s-%s-%s.AppImage", binaryName(c), c.App.Version, a.Arch))
	case "flatpak":
		out = filepath.Join(dir, fmt.Sprintf("%s-%s.flatpak", binaryName(c), c.App.Version))
	case "snap":
		out = filepath.Join(dir, fmt.Sprintf("%s_%s_%s.snap", snapName(c), c.App.Version, a.Arch))
	}
	if o.output != "" {
		out, _ = filepath.Abs(o.output)
	}
	if kind == "deb" || kind == "rpm" {
		done := progress.Begin("Packing the ." + kind)
		defer func() { done(err) }()
	}
	switch kind {
	case "deb":
		err = linuxpkg.Deb(a, out)
	case "rpm":
		err = linuxpkg.RPM(a, out)
	case "appimage":
		work, werr := os.MkdirTemp("", "giode-appimage-")
		if werr != nil {
			return "", werr
		}
		defer os.RemoveAll(work)
		err = linuxpkg.AppImage(a, work, out, verbose)
	case "flatpak":
		manifestDir := filepath.Join(dir, "flatpak")
		built, ferr := linuxpkg.Flatpak(a, manifestDir, out, verbose)
		if ferr != nil {
			return "", ferr
		}
		if !built {
			progress.Logf("flatpak-builder isn't installed; wrote the manifest to %s (flatpak-builder --user --install-deps-from=flathub build %s.json)", manifestDir, c.LinuxID())
			return filepath.Join(manifestDir, c.LinuxID()+".json"), nil
		}
	case "snap":
		project := filepath.Join(dir, "snap")
		s := c.Linux.Snap
		built, serr := linuxpkg.Snap(a, linuxpkg.SnapOptions{Name: snapName(c), Plugs: s.Plugs, Grade: s.Grade}, project, out, verbose)
		if serr != nil {
			return "", serr
		}
		if !built {
			progress.Logf("snapcraft isn't installed; wrote the project to %s (snapcraft pack there)", relPath(project))
			return filepath.Join(project, "snap", "snapcraft.yaml"), nil
		}
	}
	if err != nil {
		return "", err
	}
	return out, nil
}

// snapName is linux.snap.name, or one made from the app's name.
func snapName(c *config.Config) string {
	if c.Linux.Snap.Name != "" {
		return c.Linux.Snap.Name
	}
	return linuxpkg.SnapName(c.App.Name)
}

func debArch(goarch string) string {
	switch goarch {
	case "386":
		return "i386"
	case "arm":
		return "armhf"
	}
	return goarch
}

// buildMSIX builds the Windows executable and packages it as MSIX,
// signed with windows.signing when building for release.
func buildMSIX(c *config.Config, o buildOpts) (string, error) {
	exeOpts := o
	exeOpts.output = ""
	exeOpts.release = false // the package is signed, not the program inside
	exe, err := buildTarget(c, "windows", exeOpts)
	if err != nil {
		return "", err
	}
	arch := windowsArch(c, o)
	out := filepath.Join(filepath.Dir(exe), fmt.Sprintf("%s-%s-%s.msix", binaryName(c), c.App.Version, arch))
	if o.output != "" {
		out, _ = filepath.Abs(o.output)
	}
	m := c.Windows.MSIX
	name := m.IdentityName
	if name == "" {
		name = c.App.ID
	}
	display := m.PublisherDisplayName
	if display == "" {
		display = c.App.Publisher
	}
	var protocols []string
	protocols = append(protocols, c.App.Schemes...)
	dlls, err := windowsDLLs(c, exe)
	if err != nil {
		return "", err
	}
	var msixTypes []msix.FileType
	for _, ft := range c.WindowsFileTypes() {
		msixTypes = append(msixTypes, msix.FileType{Name: strings.TrimPrefix(ft.Extensions[0], "."), DisplayName: ft.Name, Extensions: ft.Extensions})
	}
	inst := c.Windows.Installer
	ports, err := inst.ParseFirewallPorts()
	if err != nil {
		return "", err
	}
	var msixPorts []msix.FirewallPort
	for _, p := range ports {
		msixPorts = append(msixPorts, msix.FirewallPort{Protocol: p.Protocol, PortMin: p.PortMin, PortMax: p.PortMax})
	}
	var actions []msix.ContextAction
	for _, a := range c.ContextActions {
		actions = append(actions, msix.ContextAction{ID: a.ID, Label: a.Label, Files: a.Files, Extensions: a.Extensions, Folders: a.Folders, Background: a.Background})
	}
	done := progress.Begin("Packing the MSIX")
	err = msix.Write(msix.App{
		Name: c.App.Name, IdentityName: name, Publisher: m.Publisher, PublisherDisplayName: display,
		Description: c.App.Description, Version: c.App.Version, Exe: exe, Icon: c.Path(c.App.Icon),
		Arch: arch, Protocols: protocols, Files: dlls, FileTypes: msixTypes, StartupTask: m.StartupTask,
		Firewall: inst.Firewall, FirewallPublic: inst.FirewallPublic, FirewallPorts: msixPorts,
		ContextActions: actions,
	}, out)
	done(err)
	if err != nil {
		return "", err
	}
	if o.release {
		if err := signWindows(c, out); err != nil {
			return "", err
		}
	} else {
		progress.Logf("the package is unsigned; Windows installs only signed MSIX packages (use -release with windows.signing)")
	}
	return out, nil
}

// buildWinget builds the setup program and writes winget manifests for
// it.
func buildWinget(c *config.Config, o buildOpts) (string, error) {
	setupOpts := o
	setupOpts.output = ""
	setup, err := buildTarget(c, "windows-installer", setupOpts)
	if err != nil {
		return "", err
	}
	w := c.Windows.Winget
	id := w.ID
	if id == "" {
		id = wingetName(c.App.Publisher) + "." + wingetName(c.App.Name)
	}
	arch := o.arch
	if arch == "" {
		arch = c.Windows.Arch
	}
	dir := filepath.Join(filepath.Dir(setup), "winget")
	if o.output != "" {
		dir, _ = filepath.Abs(o.output)
	}
	done := progress.Begin("Writing the winget manifests")
	out, err := winget.Write(winget.App{
		ID: id, Name: c.App.Name, Version: c.App.Version, Publisher: c.App.Publisher,
		Description: c.App.Description, License: w.License, Website: c.App.Website, ProductCode: c.App.ID,
		Installer: setup, InstallerURL: w.InstallerURL, PerMachine: c.Windows.Installer.PerMachine, Arch: arch,
	}, dir)
	done(err)
	return out, err
}

// wingetName makes an identifier part from a name: letters and digits.
func wingetName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "App"
	}
	return b.String()
}

// buildMAS builds the app for the Mac App Store: signed with the Apple
// Distribution certificate through the store's provisioning profile
// (embedded), with the sandbox's entitlements, in an installer package
// signed with the Mac installer certificate, the format Transporter
// takes.
func buildMAS(c *config.Config, o buildOpts) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("Mac App Store builds need macOS (codesign, productbuild)")
	}
	if _, ok := c.MacOS.Plist["LSApplicationCategoryType"]; !ok {
		return "", fmt.Errorf("the Mac App Store needs a category: set LSApplicationCategoryType in [macos.plist], like \"public.app-category.utilities\"")
	}
	identity := c.MacOS.Store.InstallerIdentity
	if identity == "" {
		var err error
		if identity, err = installerIdentity(); err != nil {
			return "", err
		}
	}
	appOpts := o
	appOpts.output = ""
	app, err := buildTarget(c, "mas-app", appOpts)
	if err != nil {
		return "", err
	}
	out := filepath.Join(filepath.Dir(app), fmt.Sprintf("%s-%s.pkg", binaryName(c), c.App.Version))
	if o.output != "" {
		out, _ = filepath.Abs(o.output)
	}
	os.Remove(out)
	if err := run(exec.Command("productbuild", "--component", app, "/Applications", "--sign", identity, out)); err != nil {
		return "", err
	}
	return out, nil
}

// installerIdentity finds the Mac installer certificate in the keychain.
func installerIdentity() (string, error) {
	out, err := output("security", "find-identity", "-v", "-p", "basic")
	if err != nil {
		return "", err
	}
	if id := findInstallerIdentity(out); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("no Mac installer certificate (\"3rd Party Mac Developer Installer\" or \"Mac Installer Distribution\") in the keychain: create one at developer.apple.com, or set macos.store.installer_identity")
}

// findInstallerIdentity picks the installer identity from security
// find-identity's output.
func findInstallerIdentity(list string) string {
	for _, line := range strings.Split(list, "\n") {
		i, j := strings.Index(line, `"`), strings.LastIndex(line, `"`)
		if i < 0 || j <= i {
			continue
		}
		name := line[i+1 : j]
		if strings.HasPrefix(name, "3rd Party Mac Developer Installer:") || strings.HasPrefix(name, "Mac Installer Distribution:") {
			return name
		}
	}
	return ""
}

// buildDMG builds the macOS app and puts it in a disk image with a link
// to Applications, signed and notarized like the app under -release.
func buildDMG(c *config.Config, o buildOpts) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("disk images need macOS (hdiutil)")
	}
	appOpts := o
	appOpts.output = ""
	app, err := buildTarget(c, "macos", appOpts)
	if err != nil {
		return "", err
	}
	out := filepath.Join(filepath.Dir(app), fmt.Sprintf("%s-%s-%s.dmg", binaryName(c), c.App.Version, macArch(o)))
	if o.output != "" {
		out, _ = filepath.Abs(o.output)
	}
	stage, err := os.MkdirTemp("", "giode-dmg-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err := run(exec.Command("ditto", app, filepath.Join(stage, filepath.Base(app)))); err != nil {
		return "", err
	}
	if err := os.Symlink("/Applications", filepath.Join(stage, "Applications")); err != nil {
		return "", err
	}
	os.Remove(out)
	if err := createDMG(c.App.Name, stage, out); err != nil {
		return "", err
	}
	if o.release && c.MacOS.Identity != "" {
		if err := run(exec.Command("codesign", "--force", "--sign", c.MacOS.Identity, out)); err != nil {
			return "", err
		}
		if c.MacOS.NotaryID != "" {
			args := []string{"notarytool", "submit", out, "--apple-id", c.MacOS.NotaryID, "--team-id", c.MacOS.NotaryTeamID, "--wait"}
			if pass := os.Getenv(c.MacOS.NotaryPassEnv); pass != "" {
				args = append(args, "--password", pass)
			}
			if err := run(exec.Command("xcrun", args...)); err != nil {
				return "", err
			}
			if err := run(exec.Command("xcrun", "stapler", "staple", out)); err != nil {
				return "", err
			}
		}
	}
	return out, nil
}

// windowsArch is the CPU a Windows build is for: -arch, windows.arch,
// or amd64.
func windowsArch(c *config.Config, o buildOpts) string {
	if o.arch != "" {
		return o.arch
	}
	if c.Windows.Arch != "" {
		return c.Windows.Arch
	}
	return "amd64"
}

// macArch names the CPUs of a macOS build: universal (the default, for
// Apple silicon and Intel) or the -arch list.
func macArch(o buildOpts) string {
	if o.arch == "" || strings.Contains(o.arch, ",") {
		return "universal"
	}
	return o.arch
}
