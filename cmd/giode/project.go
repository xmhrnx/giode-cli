package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/xmhrnx/giode-cli/internal/config"
	"github.com/xmhrnx/giode-cli/internal/progress"
)

// verbose prints the commands giode runs.
var verbose bool

// loadProject finds and loads giode.toml from dir upward.
func loadProject(dir string) (*config.Config, error) {
	p, err := config.Find(dir)
	if err != nil {
		return nil, err
	}
	return config.Load(p)
}

// binaryName is the app name as a file name: "My Notes" gives
// "MyNotes".
func binaryName(c *config.Config) string {
	var b strings.Builder
	for _, r := range c.App.Name {
		switch {
		case r == ' ' || r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "app"
	}
	return b.String()
}

// outDir returns dist/<platform> for the project, creating it.
func outDir(c *config.Config, platform string) (string, error) {
	d := filepath.Join(c.Path(c.App.Output), platform)
	return d, os.MkdirAll(d, 0o755)
}

// pkgPath is the main package as gogio and go build take it, run from
// the project directory.
func pkgPath(c *config.Config) string {
	p := c.App.Package
	if p == "." || p == "" {
		return "."
	}
	if strings.HasPrefix(p, ".") || filepath.IsAbs(p) {
		return p
	}
	return "./" + p
}

// command returns a command run in the project directory, its output
// going to the terminal.
func command(c *config.Config, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	if c != nil {
		cmd.Dir = c.Dir
	}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd
}

// run runs cmd, printing it first with -x.
func run(cmd *exec.Cmd) error {
	line := commandLine(cmd.Args)
	if verbose {
		fmt.Fprintln(os.Stderr, "+", line)
	}
	// Output nobody takes is kept and shown if the command fails: a nil
	// Stdout or Stderr would drop it (hdiutil's, ditto's, codesign's
	// reason for failing), and while the build's progress redraws the
	// terminal, output meant for it is kept too.
	var captured bytes.Buffer
	live := progress.Live()
	if cmd.Stdout == nil || live && cmd.Stdout == os.Stdout {
		cmd.Stdout = &captured
	}
	if cmd.Stderr == nil || live && cmd.Stderr == os.Stderr {
		cmd.Stderr = &captured
	}
	done := progress.Command(cmd)
	err := cmd.Run()
	done(err)
	if err != nil {
		if out := strings.TrimSpace(captured.String()); out != "" {
			return fmt.Errorf("%s: %w\n%s", line, err, out)
		}
		return fmt.Errorf("%s: %w", line, err)
	}
	return nil
}

// commandLine joins a command's arguments for messages, hiding the value
// after --password.
func commandLine(args []string) string {
	shown := append([]string(nil), args...)
	for i := 1; i < len(shown); i++ {
		if shown[i-1] == "--password" {
			shown[i] = "***"
		}
	}
	return strings.Join(shown, " ")
}

// output runs cmd and returns its trimmed standard output.
func output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if verbose {
		fmt.Fprintln(os.Stderr, "+", strings.Join(cmd.Args, " "))
	}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", strings.Join(cmd.Args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// androidHome returns the Android SDK directory from ANDROID_HOME or
// ANDROID_SDK_ROOT, or the platform's default install location.
func androidHome() string {
	for _, v := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if d := os.Getenv(v); d != "" {
			return d
		}
	}
	home, _ := os.UserHomeDir()
	var d string
	switch runtime.GOOS {
	case "darwin":
		d = filepath.Join(home, "Library", "Android", "sdk")
	case "windows":
		d = filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk")
	default:
		d = filepath.Join(home, "Android", "Sdk")
	}
	if _, err := os.Stat(d); err == nil {
		return d
	}
	return ""
}

// ensureAndroidHome sets ANDROID_HOME for gogio when only the default
// location has the SDK.
func ensureAndroidHome() error {
	d := androidHome()
	if d == "" {
		return fmt.Errorf("the Android SDK wasn't found: set ANDROID_HOME (giode doctor explains what's needed)")
	}
	return os.Setenv("ANDROID_HOME", d)
}

// adb returns the adb command, from the SDK or the PATH.
func adb() (string, error) {
	if d := androidHome(); d != "" {
		p := filepath.Join(d, "platform-tools", "adb"+exe())
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	p, err := exec.LookPath("adb")
	if err != nil {
		return "", fmt.Errorf("adb wasn't found: install the Android SDK platform-tools")
	}
	return p, nil
}

func exe() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// relPath shortens p to be relative to the working directory when
// that's shorter.
func relPath(p string) string {
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	if r, err := filepath.Rel(wd, p); err == nil && len(r) < len(p) {
		return r
	}
	return p
}
