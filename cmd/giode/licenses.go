package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xmhrnx/giode-cli/internal/config"
	"github.com/xmhrnx/giode-cli/internal/licenses"
)

const licensesUsage = `
Usage: giode licenses [options]

Writes the licenses of the Go modules the app is built from - on any
platform - and of Go's standard library to a JSON file (app.licenses in
giode.toml, assets/licenses.json by default), for the open-source
notices apps must show. Embed the file and show it with
giode.LicensesView. With app.licenses set, every build refreshes it.
`

func cmdLicenses(args []string) error {
	fs := flags("licenses", licensesUsage)
	dir := fs.String("C", ".", "project directory")
	out := fs.String("o", "", "output file (default app.licenses, or assets/licenses.json)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	c, err := loadProject(*dir)
	if err != nil {
		return err
	}
	path := *out
	if path == "" {
		path = c.App.Licenses
	}
	if path == "" {
		path = filepath.Join("assets", "licenses.json")
	}
	n, err := writeLicenses(c, c.Path(path))
	if err != nil {
		return err
	}
	fmt.Printf("Wrote %s: %d licenses.\n", relPath(c.Path(path)), n)
	return nil
}

// writeLicenses collects the app's licenses into path.
func writeLicenses(c *config.Config, path string) (int, error) {
	ls, err := licenses.Collect(c.Dir, pkgPath(c), c.App.Tags)
	if err != nil {
		return 0, err
	}
	for _, l := range ls {
		if l.Text == "" {
			fmt.Fprintf(os.Stderr, "  note: %s has no license file\n", l.Module)
		}
	}
	b, err := json.MarshalIndent(ls, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	return len(ls), os.WriteFile(path, append(b, '\n'), 0o644)
}
