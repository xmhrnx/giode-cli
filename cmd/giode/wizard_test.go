package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/xmhrnx/giode-cli/internal/prompt"
	"github.com/xmhrnx/giode-cli/internal/scaffold"
)

func TestWizard(t *testing.T) {
	// Folder; platforms: Linux starts checked on Linux - uncheck it
	// (down twice, space), check Android and iOS, Enter; then name
	// (Enter), a bad app ID then a good one, version (Enter), module
	// (Enter), publisher, description, website (Enter), confirm (Enter).
	keys := "notes-app\n" +
		"\x1b[B\x1b[B \x1b[B \x1b[B \r" +
		"\n" + "not an id\ncom.acme.notes\n" + "\n" + "\n" + "Acme\n" + "Quick notes\n" + "\n" + "\n"
	var out bytes.Buffer
	term := prompt.New(strings.NewReader(keys), &out)
	term.Raw = func() (func(), error) { return func() {}, nil }
	p, err := wizard(term, scaffold.Project{}, true)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	// The host's own desktop starts checked; whatever it is, the
	// presses above leave Android and iOS checked.
	if !contains(p.Platforms, "android") || !contains(p.Platforms, "ios") {
		t.Errorf("platforms %v", p.Platforms)
	}
	want := scaffold.Project{Dir: "notes-app", Name: "Notes App", Module: "notes-app", AppID: "com.acme.notes",
		Version: "0.1.0", Publisher: "Acme", Description: "Quick notes", Platforms: p.Platforms}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got  %+v\nwant %+v", p, want)
	}
	if !strings.Contains(out.String(), "an app ID is reverse-DNS") {
		t.Error("the bad app ID wasn't refused")
	}
	for _, pl := range scaffold.Platforms {
		if !strings.Contains(out.String(), pl.Name) {
			t.Errorf("%s isn't offered", pl.Name)
		}
	}
}

func TestPlatformItems(t *testing.T) {
	items := platformItems("linux", nil)
	if len(items) != len(scaffold.Platforms) {
		t.Fatal("every platform is listed")
	}
	for i, it := range items {
		id := scaffold.Platforms[i].ID
		if it.Checked != (id == "linux") {
			t.Errorf("%s checked = %v on Linux", id, it.Checked)
		}
		if (id == "macos" || id == "ios") && !strings.Contains(it.Hint, "not this computer") {
			t.Errorf("%s hint %q", id, it.Hint)
		}
	}
	if items := platformItems("darwin", []string{"web"}); items[5].Checked != true || items[1].Checked {
		t.Error("preset platforms replace the default")
	}
}
