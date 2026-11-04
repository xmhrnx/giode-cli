package config

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ContextAction is an entry the app adds to the file manager's
// right-click menu; see giode.ContextAction, which these are passed to.
type ContextAction struct {
	ID         string   `toml:"id" json:"id"`
	Label      string   `toml:"label" json:"label"`
	Files      bool     `toml:"files" json:"files,omitempty"`
	Extensions []string `toml:"extensions" json:"extensions,omitempty"`
	Folders    bool     `toml:"folders" json:"folders,omitempty"`
	Background bool     `toml:"background" json:"background,omitempty"`
}

// OnFiles reports whether the action is offered on files.
func (c ContextAction) OnFiles() bool {
	return c.Files || len(c.Extensions) > 0 || (!c.Folders && !c.Background)
}

var contextIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (c *Config) validateContextActions() []error {
	var errs []error
	seen := map[string]bool{}
	for i, a := range c.ContextActions {
		switch {
		case !contextIDRE.MatchString(a.ID):
			errs = append(errs, fmt.Errorf("context_actions[%d].id %q must be letters, digits, - and _", i, a.ID))
		case seen[a.ID]:
			errs = append(errs, fmt.Errorf("context_actions: id %q is used twice", a.ID))
		}
		seen[a.ID] = true
		if a.Label == "" {
			errs = append(errs, fmt.Errorf("context_actions[%d].label is required", i))
		}
	}
	return errs
}

// ContextActionsLdflag returns the linker flag handing the context
// actions to the giode library, or "" without any.
func (c *Config) ContextActionsLdflag() string {
	if len(c.ContextActions) == 0 {
		return ""
	}
	b, _ := json.Marshal(c.ContextActions)
	return "-X github.com/xmhrnx/giode.buildContextActions=" + base64.URLEncoding.EncodeToString(b)
}

// ContextActionPrefix names the app's context menu entries, as the
// giode library does: the app ID with anything but letters, digits, -
// and _ replaced by -.
func (c *Config) ContextActionPrefix() string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '-'
	}, c.App.ID)
}

// WindowsVerb is an Explorer context menu entry: a key under
// Software\Classes, its label, and the action and path argument its
// command passes. CLSID, for actions on files and folders, is the COM
// class the giode library registers to take the whole selection in one
// launch (a DropTarget verb): Explorer would otherwise start the app
// once per selected item.
type WindowsVerb struct {
	Key, Label, ID, Arg, CLSID string
}

// ContextActionCLSID is the giode library's giode.ContextActionCLSID:
// a name-based UUID (version 5) of "giode-context-action:" + prefix +
// "." + id. The two must agree.
func ContextActionCLSID(prefix, id string) string {
	h := sha1.Sum([]byte("giode-context-action:" + prefix + "." + id))
	b := h[:16]
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("{%x-%x-%x-%x-%x}", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// WindowsVerbs returns the Explorer entries for the context actions.
func (c *Config) WindowsVerbs() []WindowsVerb {
	var out []WindowsVerb
	prefix := c.ContextActionPrefix()
	for _, a := range c.ContextActions {
		add := func(parent, arg string) {
			v := WindowsVerb{Key: parent + `\shell\` + prefix + "." + a.ID, Label: a.Label, ID: a.ID, Arg: arg}
			if arg == "%1" {
				v.CLSID = ContextActionCLSID(prefix, a.ID)
			}
			out = append(out, v)
		}
		if a.OnFiles() {
			if len(a.Extensions) == 0 {
				add("*", "%1")
			}
			for _, ext := range a.Extensions {
				if !strings.HasPrefix(ext, ".") {
					ext = "." + ext
				}
				add(`SystemFileAssociations\`+ext, "%1")
			}
		}
		if a.Folders {
			add("Directory", "%1")
		}
		if a.Background {
			add(`Directory\Background`, "%V")
		}
	}
	return out
}

// macServices returns the NSServices entries for the context actions:
// Finder lists them under Quick Actions, and macOS sends the chosen
// files to the giode library's provider.
func (c *Config) macServices() []any {
	var out []any
	for _, a := range c.ContextActions {
		var types []any
		if a.OnFiles() {
			if len(a.Extensions) == 0 {
				types = append(types, "public.data")
			}
			for _, ext := range a.Extensions {
				types = append(types, utiForExtension(strings.TrimPrefix(ext, ".")))
			}
		}
		if a.Folders || a.Background {
			types = append(types, "public.folder")
		}
		out = append(out, map[string]any{
			"NSMenuItem":        map[string]any{"default": a.Label},
			"NSMessage":         "giodeContextAction",
			"NSPortName":        c.App.Name,
			"NSUserData":        a.ID,
			"NSRequiredContext": map[string]any{},
			"NSSendFileTypes":   types,
		})
	}
	return out
}

// utiForExtension maps common extensions to their uniform type
// identifiers; others get a dynamic one Finder resolves from the
// extension.
func utiForExtension(ext string) string {
	switch strings.ToLower(ext) {
	case "txt":
		return "public.plain-text"
	case "md", "markdown":
		return "net.daringfireball.markdown"
	case "pdf":
		return "com.adobe.pdf"
	case "png":
		return "public.png"
	case "jpg", "jpeg":
		return "public.jpeg"
	case "json":
		return "public.json"
	case "html", "htm":
		return "public.html"
	case "csv":
		return "public.comma-separated-values-text"
	case "go", "js", "py", "ts", "c", "h", "rs", "java", "swift":
		return "public.source-code"
	}
	return "public.data"
}
