// Package sign signs Windows executables and installers with
// Authenticode.
package sign

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/xmhrnx/giode-cli/internal/config"
)

// Verbose prints the signing commands, with the password hidden.
var Verbose bool

// Windows signs file in place with the configured certificate or
// command. description is the name Windows shows in its prompts.
func Windows(c *config.Config, file, description string) error {
	s := c.Windows.Signing
	if len(s.Command) > 0 {
		args := make([]string, len(s.Command))
		for i, a := range s.Command {
			args[i] = strings.ReplaceAll(a, "{file}", file)
		}
		return run(exec.Command(args[0], args[1:]...), "")
	}
	if s.Certificate == "" {
		return errors.New("windows.signing has no certificate or command")
	}
	cert := c.Path(s.Certificate)
	if _, err := os.Stat(cert); err != nil {
		return fmt.Errorf("windows.signing.certificate: %w", err)
	}
	pass := os.Getenv(s.PasswordEnv)
	if s.Timestamp == "none" {
		s.Timestamp = ""
	}
	if runtime.GOOS == "windows" {
		tool, err := signtool()
		if err != nil {
			return err
		}
		args := []string{"sign", "/fd", "SHA256", "/f", cert}
		if pass != "" {
			args = append(args, "/p", pass)
		}
		if s.Timestamp != "" {
			args = append(args, "/tr", s.Timestamp, "/td", "SHA256")
		}
		if description != "" {
			args = append(args, "/d", description)
		}
		return run(exec.Command(tool, append(args, file)...), pass)
	}
	tool, err := exec.LookPath("osslsigncode")
	if err != nil {
		return errors.New("signing Windows files here needs osslsigncode (apt install osslsigncode, brew install osslsigncode)")
	}
	out := file + ".signed"
	args := []string{"sign", "-pkcs12", cert, "-h", "sha256"}
	if pass != "" {
		args = append(args, "-pass", pass)
	}
	if s.Timestamp != "" {
		args = append(args, "-ts", s.Timestamp)
	}
	if description != "" {
		args = append(args, "-n", description)
	}
	if c.App.Website != "" {
		args = append(args, "-i", c.App.Website)
	}
	args = append(args, "-in", file, "-out", out)
	if err := run(exec.Command(tool, args...), pass); err != nil {
		os.Remove(out)
		if s.Timestamp != "" {
			err = fmt.Errorf("%w\n(if %s can't be reached from here, set windows.signing.timestamp to another server, or to \"none\")", err, s.Timestamp)
		}
		return err
	}
	if st, err := os.Stat(file); err == nil {
		os.Chmod(out, st.Mode().Perm())
	}
	return os.Rename(out, file)
}

// signtool finds signtool.exe on the PATH or in the Windows SDK.
func signtool() (string, error) {
	if p, err := exec.LookPath("signtool"); err == nil {
		return p, nil
	}
	var found []string
	for _, root := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		if root == "" {
			continue
		}
		m, _ := filepath.Glob(filepath.Join(root, "Windows Kits", "10", "bin", "*", "x64", "signtool.exe"))
		found = append(found, m...)
	}
	if len(found) == 0 {
		return "", errors.New("signtool.exe wasn't found: install the Windows SDK (its \"Signing Tools\" part)")
	}
	sort.Strings(found)
	return found[len(found)-1], nil
}

// run runs cmd, hiding secret from what's printed.
func run(cmd *exec.Cmd, secret string) error {
	line := strings.Join(cmd.Args, " ")
	if secret != "" {
		line = strings.ReplaceAll(line, secret, "***")
	}
	if Verbose {
		fmt.Fprintln(os.Stderr, "+", line)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "***")
		}
		return fmt.Errorf("%s: %v\n%s", line, err, msg)
	}
	return nil
}
