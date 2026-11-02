package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xmhrnx/giode-cli/internal/sign"
)

const signUsage = `
Usage: giode sign <file>...

Signs Windows executables and installers with the Authenticode
certificate in giode.toml's [windows.signing], in place: with signtool
on Windows and osslsigncode elsewhere, or with your own command for keys
in a hardware token or a cloud service. giode build -release does this
for what it builds; use this for other files.

	[windows.signing]
	certificate = "codesign.pfx"        # password in $GIODE_WINDOWS_CERT_PASS
	timestamp = "http://timestamp.digicert.com"
	# command = ["AzureSignTool", "sign", "-kvu", "https://...", "-kvc", "cert", "-tr", "http://timestamp.digicert.com", "{file}"]
`

func cmdSign(args []string) error {
	fs := flags("sign", signUsage)
	dir := fs.String("C", ".", "project directory")
	fs.BoolVar(&verbose, "x", false, "print the commands run")
	files, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fs.Usage()
		return errors.New("name the files to sign")
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if !c.Windows.Signing.Configured() {
		return errors.New("giode.toml has no [windows.signing] certificate or command (giode cert makes a test certificate)")
	}
	sign.Verbose = verbose
	for _, f := range files {
		if err := sign.Windows(c, f, c.App.Name); err != nil {
			return err
		}
		fmt.Println("Signed", f)
	}
	return nil
}

const certUsage = `
Usage: giode cert [options]

Creates a self-signed Authenticode code signing certificate (.pfx) and
points windows.signing.certificate in giode.toml at it. The password is
read from $GIODE_WINDOWS_CERT_PASS (or windows.signing.password_env).

Windows doesn't trust a self-signed certificate: SmartScreen still warns
on other computers. Use it to test the signing setup, or on computers
that trust it (Import-Certificate into Cert:\LocalMachine\Root and
TrustedPublisher). For public distribution, buy a code signing
certificate from a certificate authority, or use Azure Trusted Signing
with windows.signing.command.
`

func cmdCert(args []string) error {
	fs := flags("cert", certUsage)
	dir := fs.String("C", ".", "project directory")
	out := fs.String("o", "codesign.pfx", "certificate file")
	subject := fs.String("subject", "", "certificate name (default: app.publisher)")
	years := fs.Int("years", 3, "validity in years")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	env := c.Windows.Signing.PasswordEnv
	pass := os.Getenv(env)
	if pass == "" {
		return fmt.Errorf("set $%s to the certificate password", env)
	}
	path := c.Path(*out)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if *subject == "" {
		*subject = c.App.Publisher
	}
	if err := sign.SelfSigned(path, *subject, pass, *years); err != nil {
		return err
	}
	if c.Windows.Signing.Certificate == "" {
		if err := setTOMLValue(filepath.Join(c.Dir, "giode.toml"), "windows.signing", "certificate", filepath.ToSlash(*out)); err != nil {
			return fmt.Errorf("created %s, but couldn't update giode.toml: %w", path, err)
		}
		fmt.Printf("Created %s (CN=%s, %d years) and set windows.signing.certificate in giode.toml.\n", relPath(path), *subject, *years)
	} else {
		fmt.Printf("Created %s. giode.toml still points at %s.\n", relPath(path), c.Windows.Signing.Certificate)
	}
	fmt.Println("Sign builds with: giode build -release windows-installer")
	return nil
}

// setTOMLValue adds key = value to a table of giode.toml, creating the
// table at the end when there's none. The file's line endings are kept
// (a giode.toml checked out on Windows can have CRLF ones).
func setTOMLValue(tomlPath, table, key, value string) error {
	b, err := os.ReadFile(tomlPath)
	if err != nil {
		return err
	}
	s := string(b)
	nl := "\n"
	if strings.Contains(s, "\r\n") {
		nl = "\r\n"
	}
	line := fmt.Sprintf("%s = %q", key, value) + nl
	header := "[" + table + "]"
	lines := strings.SplitAfter(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != header {
			continue
		}
		// After the table's lines, before the next table or blank line,
		// so it follows the commented examples.
		at := i + 1
		for at < len(lines) {
			t := strings.TrimSpace(lines[at])
			if t == "" || strings.HasPrefix(t, "[") {
				break
			}
			at++
		}
		if at == len(lines) && !strings.HasSuffix(s, "\n") {
			line = nl + line
		}
		out := strings.Join(lines[:at], "") + line + strings.Join(lines[at:], "")
		return os.WriteFile(tomlPath, []byte(out), 0o644)
	}
	s = strings.TrimRight(s, "\r\n") + nl + nl + header + nl + line
	return os.WriteFile(tomlPath, []byte(s), 0o644)
}
