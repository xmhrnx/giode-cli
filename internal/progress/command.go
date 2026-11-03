package progress

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// Command starts a step for an external command about to run, named
// for what it does ("Compiling Go · arm64"), and returns the function
// ending it. Quick bookkeeping commands get no step.
func Command(cmd *exec.Cmd) func(error) {
	if current.Load() == nil {
		return func(error) {}
	}
	label := CommandLabel(cmd)
	if label == "" {
		return func(error) {}
	}
	return Begin(label)
}

// quiet are commands too quick and frequent to be worth a line.
var quiet = map[string]bool{
	"plutil": true, "PlistBuddy": true, "security": true, "install_name_tool": true,
	"pkg-config": true, "otool": true, "file": true, "which": true, "git": true,
}

// CommandLabel names what a command does, or "" for one not worth
// showing.
func CommandLabel(cmd *exec.Cmd) string {
	if len(cmd.Args) == 0 {
		return ""
	}
	tool := strings.TrimSuffix(filepath.Base(cmd.Args[0]), ".exe")
	args := cmd.Args[1:]
	if tool == "xcrun" && len(args) > 0 {
		// xcrun runs the tool named next, after its own options.
		for len(args) > 0 && strings.HasPrefix(args[0], "-") {
			if args[0] == "--sdk" || args[0] == "-sdk" {
				args = args[1:]
			}
			args = args[1:]
		}
		if len(args) == 0 {
			return ""
		}
		tool, args = args[0], args[1:]
		if tool == "--show-sdk-path" || tool == "--find" {
			return ""
		}
	}
	if quiet[tool] {
		return ""
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch tool {
	case "go":
		switch sub {
		case "build":
			return "Compiling Go" + targetSuffix(cmd)
		case "list", "env", "version", "mod":
			return ""
		}
		return "go " + sub
	case "javac":
		return "Compiling Java"
	case "d8":
		return "Converting to DEX"
	case "aapt2":
		if sub == "link" {
			return "Linking resources"
		}
		if sub == "compile" {
			return "Compiling resources"
		}
		return ""
	case "zipalign":
		return "Aligning the APK"
	case "apksigner":
		if sub == "verify" {
			return ""
		}
		return "Signing the APK"
	case "jarsigner":
		return "Signing the bundle"
	case "java":
		if strings.Contains(strings.Join(args, " "), "bundletool") {
			return "Building the App Bundle"
		}
		return "Running Java"
	case "bundletool":
		return "Building the App Bundle"
	case "lipo":
		if sub == "-info" {
			return ""
		}
		return "Merging architectures"
	case "clang", "cc", "gcc":
		return "Compiling C" + targetSuffix(cmd)
	case "actool":
		return "Compiling the asset catalog"
	case "codesign":
		if sub == "-d" || sub == "--verify" || sub == "-v" {
			return ""
		}
		return "Code signing " + filepath.Base(cmd.Args[len(cmd.Args)-1])
	case "notarytool":
		return "Notarizing (Apple's servers take a few minutes)"
	case "stapler":
		return "Stapling the notarization"
	case "hdiutil":
		return "Creating the disk image"
	case "ditto":
		return "Copying the app"
	case "makensis":
		return "Building the setup program (NSIS)"
	case "signtool", "osslsigncode":
		return "Authenticode signing " + filepath.Base(cmd.Args[len(cmd.Args)-1])
	case "appimagetool":
		return "Building the AppImage"
	case "flatpak-builder":
		return "Building the Flatpak"
	case "flatpak":
		return "Bundling the Flatpak"
	case "keytool":
		return ""
	}
	return "Running " + tool
}

// targetSuffix is " · arm64" (or "windows/amd64") from a build
// command's environment.
func targetSuffix(cmd *exec.Cmd) string {
	var goos, goarch string
	for _, e := range cmd.Env {
		switch {
		case strings.HasPrefix(e, "GOOS="):
			goos = e[5:]
		case strings.HasPrefix(e, "GOARCH="):
			goarch = e[7:]
		}
	}
	for i, a := range cmd.Args {
		if a == "-arch" && i+1 < len(cmd.Args) {
			goarch = cmd.Args[i+1]
		}
	}
	switch {
	case goarch == "":
		return ""
	case goos == "" || goos == "js" || goos == "android" || goos == "ios":
		return " · " + goarch
	}
	return " · " + goos + "/" + goarch
}
