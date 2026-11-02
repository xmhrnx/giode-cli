package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/xmhrnx/giode-cli/internal/config"
	"github.com/xmhrnx/giode-cli/internal/giopatch"
)

// gioBuild is how a build uses Gio: the go.mod of the patched copy
// (-modfile; empty when not patched), the build tags, and a cleanup.
type gioBuild struct {
	modfile string
	tags    string
	cleanup func()
}

// goArgs returns the -modfile and -tags flags for go build or go run.
func (b gioBuild) goArgs() []string {
	var a []string
	if b.modfile != "" {
		a = append(a, "-modfile="+b.modfile)
	}
	if b.tags != "" {
		a = append(a, "-tags", b.tags)
	}
	return a
}

// prepareGio readies Gio for a build of target: giode's patched copy,
// with the giode_gio tag that reaches its hooks (Config.Underlay), and
// the nometal tag for app.renderer = "opengl" on Apple targets.
func prepareGio(c *config.Config, target string) (gioBuild, error) {
	b := gioBuild{tags: c.App.Tags, cleanup: func() {}}
	apple := target == "macos" || target == "dmg" || target == "ios" || target == "ios-sim" ||
		(target == "desktop" && runtime.GOOS == "darwin")
	if c.App.Renderer == "opengl" && apple {
		b.tags = addTag(b.tags, "nometal")
	}
	enabled := c.App.GioPatches == nil || *c.App.GioPatches
	android := target == "android" || target == "aab"
	if android && c.Android.GioPatches != nil && !*c.Android.GioPatches {
		enabled = false
	}
	if !enabled {
		return b, nil
	}
	modfile, cleanup, err := giopatch.Prepare(c.Dir, giopatch.All)
	switch {
	case errors.Is(err, giopatch.ErrSkipped):
		fmt.Fprintf(os.Stderr, "  note: building against plain Gio, without giode's patches (%v); Config.Underlay and Config.Renderer won't work\n", err)
		return b, nil
	case err != nil:
		return b, fmt.Errorf("patching Gio: %w (set app.gio_patches = false to skip)", err)
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "  building against patched Gio (%s)\n", modfile)
	}
	b.modfile, b.cleanup = modfile, cleanup
	b.tags = addTag(addTag(b.tags, giopatch.Tag), giopatch.Tag2)
	return b, nil
}

// addTag adds a build tag to a comma or space separated list.
func addTag(tags, tag string) string {
	fields := strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == ' ' })
	for _, t := range fields {
		if t == tag {
			return strings.Join(fields, ",")
		}
	}
	return strings.Join(append(fields, tag), ",")
}
