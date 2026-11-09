package msix

import (
	"crypto/sha1"
	_ "embed"
	"fmt"
	"html"
	"strings"
)

// The context menu handler (contextmenu/contextmenu.c), built by
// contextmenu/build.sh.
var (
	//go:embed contextmenu/contextmenu_x64.dll
	contextMenuX64 []byte
	//go:embed contextmenu/contextmenu_arm64.dll
	contextMenuARM64 []byte
)

// contextMenuDLL and contextMenuList are the handler's and its list's
// names in the package.
const (
	contextMenuDLL  = "GiodeContextMenu.dll"
	contextMenuList = "GiodeContextMenu.txt"
)

// ContextAction is an entry in File Explorer's context menu, as
// giode.toml's context_actions: offered on files (all, or those with
// Extensions, with the dot), folders, or a folder's background. Choosing
// it starts the app with --giode-action=<ID> and the selected paths,
// all of them in one launch.
type ContextAction struct {
	ID, Label           string
	Files               bool
	Extensions          []string
	Folders, Background bool
}

// onFiles reports whether the action is offered on files, as
// config.ContextAction does.
func (c ContextAction) onFiles() bool {
	return c.Files || len(c.Extensions) > 0 || (!c.Folders && !c.Background)
}

// contextCLSID is an action's COM class, the same for every build of
// the package.
func contextCLSID(identity, id string) string {
	h := sha1.Sum([]byte("giode msix context action\x00" + identity + "\x00" + id))
	h[6] = h[6]&0x0f | 0x50 // version 5, name-based
	h[8] = h[8]&0x3f | 0x80
	return fmt.Sprintf("{%08X-%04X-%04X-%04X-%012X}", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// contextMenuFiles are the handler DLL and its list for the package.
func contextMenuFiles(a App, arch, exe string) ([]file, error) {
	if len(a.ContextActions) == 0 {
		return nil, nil
	}
	var dll []byte
	switch arch {
	case "x64":
		dll = contextMenuX64
	case "arm64":
		dll = contextMenuARM64
	default:
		return nil, fmt.Errorf("File Explorer context actions in an MSIX package need an x64 or arm64 build, not %s", arch)
	}
	var list strings.Builder
	list.WriteString(exe + "\r\n")
	for _, c := range a.ContextActions {
		label := strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(c.Label)
		fmt.Fprintf(&list, "%s\t%s\t%s\r\n", contextCLSID(a.IdentityName, c.ID), c.ID, label)
	}
	return []file{{name: contextMenuDLL, body: dll}, {name: contextMenuList, body: []byte(list.String())}}, nil
}

// contextMenuExtensions are the application's extensions declaring the
// actions: File Explorer's menu entries (desktop4, desktop5 for folders)
// and the handler's COM classes, in a surrogate process.
func contextMenuExtensions(a App) string {
	if len(a.ContextActions) == 0 {
		return ""
	}
	e := html.EscapeString
	// Entries by item type, in a stable order.
	type verb struct{ id, clsid string }
	var types []string
	verbs := map[string][]verb{}
	add := func(t string, v verb) {
		if _, ok := verbs[t]; !ok {
			types = append(types, t)
		}
		verbs[t] = append(verbs[t], v)
	}
	for _, c := range a.ContextActions {
		v := verb{c.ID, contextCLSID(a.IdentityName, c.ID)}
		if c.onFiles() {
			if len(c.Extensions) == 0 {
				add("*", v)
			}
			for _, x := range c.Extensions {
				if !strings.HasPrefix(x, ".") {
					x = "." + x
				}
				add(strings.ToLower(x), v)
			}
		}
		if c.Folders {
			add("Directory", v)
		}
		if c.Background {
			add(`Directory\Background`, v)
		}
	}
	var b strings.Builder
	b.WriteString("\n        <desktop4:Extension Category=\"windows.fileExplorerContextMenus\">\n          <desktop4:FileExplorerContextMenus>")
	for _, t := range types {
		ns := "desktop4"
		if strings.HasPrefix(t, "Directory") {
			ns = "desktop5"
		}
		fmt.Fprintf(&b, "\n            <%s:ItemType Type=\"%s\">", ns, e(t))
		for _, v := range verbs[t] {
			fmt.Fprintf(&b, "\n              <%s:Verb Id=\"%s\" Clsid=\"%s\"/>", ns, e(verbID(v.id)), strings.Trim(v.clsid, "{}"))
		}
		fmt.Fprintf(&b, "\n            </%s:ItemType>", ns)
	}
	b.WriteString("\n          </desktop4:FileExplorerContextMenus>\n        </desktop4:Extension>")
	fmt.Fprintf(&b, "\n        <com:Extension Category=\"windows.comServer\">\n          <com:ComServer>\n            <com:SurrogateServer DisplayName=\"%s\">", e(a.Name))
	for _, c := range a.ContextActions {
		fmt.Fprintf(&b, "\n              <com:Class Id=\"%s\" Path=\"%s\" ThreadingModel=\"STA\"/>", strings.Trim(contextCLSID(a.IdentityName, c.ID), "{}"), contextMenuDLL)
	}
	b.WriteString("\n            </com:SurrogateServer>\n          </com:ComServer>\n        </com:Extension>")
	return b.String()
}

// verbID is a manifest verb ID: letters and digits, starting with a
// letter.
func verbID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 || !(b.String()[0] >= 'A' && b.String()[0] <= 'z') {
		return "Giode" + b.String()
	}
	return b.String()
}
