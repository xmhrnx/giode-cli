package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/xmhrnx/giode-cli/internal/config"
	"github.com/xmhrnx/giode-cli/internal/prompt"
	"github.com/xmhrnx/giode-cli/internal/scaffold"
)

// terminal returns a prompt.Terminal on the standard streams, with raw
// key input and color when they're terminals.
func terminal() (*prompt.Terminal, bool) {
	t := prompt.New(os.Stdin, os.Stdout)
	in := int(os.Stdin.Fd())
	interactive := term.IsTerminal(in)
	if interactive {
		t.Raw = func() (func(), error) {
			old, err := term.MakeRaw(in)
			if err != nil {
				return nil, err
			}
			return func() { term.Restore(in, old) }, nil
		}
	}
	t.Color = term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""
	return t, interactive
}

// platformItems are the platform choices, each saying what building it
// needs on this computer; the computer's own desktop starts checked.
func platformItems(goos string, preset []string) []prompt.Item {
	host := map[string]string{"darwin": "macos", "windows": "windows"}[goos]
	if host == "" {
		host = "linux"
	}
	var items []prompt.Item
	for _, p := range scaffold.Platforms {
		it := prompt.Item{Label: p.Name, Hint: p.Needs, Checked: p.ID == host}
		if len(preset) > 0 {
			it.Checked = contains(preset, p.ID)
		}
		if len(p.BuildsOn) > 0 && !contains(p.BuildsOn, goos) {
			it.Hint += " - not this computer"
		}
		items = append(items, it)
	}
	return items
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// wizard asks for the new project's settings: the platforms, then the
// name, app ID, version and the rest, each with a default Enter takes.
// p holds the defaults (and anything given as flags).
func wizard(t *prompt.Terminal, p scaffold.Project, askDir bool) (scaffold.Project, error) {
	t.Printf("\n%s %s\n\n", t.Bold("giode new"), t.Dim("- answer, or press Enter for the suggestion in parentheses"))
	if askDir {
		dir, err := t.Input("Project folder", "my-app", func(s string) error {
			if s == "" {
				return errors.New("name a folder")
			}
			if entries, err := os.ReadDir(s); err == nil && len(entries) > 0 {
				return fmt.Errorf("%s exists and isn't empty", s)
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return nil
		})
		if err != nil {
			return p, err
		}
		p.Dir = dir
		p.Name, p.Module, p.AppID = scaffold.Defaults(dir)
	}

	items := platformItems(runtime.GOOS, p.Platforms)
	checked, err := t.Checkboxes("Which platforms is the app for?", items)
	if err != nil {
		return p, err
	}
	p.Platforms = nil
	for i, c := range checked {
		if c {
			p.Platforms = append(p.Platforms, scaffold.Platforms[i].ID)
		}
	}

	ask := func(dst *string, q string, check func(string) error) error {
		v, err := t.Input(q, *dst, check)
		*dst = v
		return err
	}
	optionalURL := func(s string) error {
		if s == "" {
			return nil
		}
		if u, err := url.Parse(s); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errors.New("a web address starts with https://")
		}
		return nil
	}
	notEmpty := func(what string) func(string) error {
		return func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New(what + " can't be empty")
			}
			return nil
		}
	}
	if p.Version == "" {
		p.Version = "0.1.0"
	}
	steps := []struct {
		dst   *string
		q     string
		check func(string) error
	}{
		{&p.Name, "App name", notEmpty("The name")},
		{&p.AppID, "App ID (reverse-DNS, can't change after publishing)", config.CheckAppID},
		{&p.Version, "Version", config.CheckVersion},
		{&p.Module, "Go module path", notEmpty("The module path")},
		{&p.Publisher, "Publisher (you or your company, optional)", nil},
		{&p.Description, "One-line description (optional)", nil},
		{&p.Website, "Website (optional)", optionalURL},
	}
	for _, s := range steps {
		if err := ask(s.dst, s.q, s.check); err != nil {
			return p, err
		}
	}

	t.Printf("\n  %s  %s\n  %s  %s (%s) %s\n  %s  %s\n\n",
		t.Dim("folder   "), p.Dir,
		t.Dim("app      "), p.Name, p.AppID, p.Version,
		t.Dim("platforms"), strings.Join(p.Platforms, ", "))
	ok, err := t.Confirm("Create the project?", true)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, prompt.ErrCanceled
	}
	return p, nil
}
