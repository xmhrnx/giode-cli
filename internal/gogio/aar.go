// giode: Android libraries (AARs and JARs) built into apps - what
// Gradle does for a dependency: their classes are dexed with the app's,
// their resources compiled and linked with R classes for their
// packages, their manifests merged into the app's, and their native
// libraries and assets packaged.

package gogio

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/xmhrnx/giode-cli/internal/maven"
	"github.com/xmhrnx/giode-cli/internal/progress"
)

// androidLibs are the AARs and JARs to build in (Options.AndroidLibs);
// androidDeps, androidRepos and mavenCache are Options'
// AndroidDependencies, AndroidRepos and MavenCache.
var (
	androidLibs  []string
	androidDeps  []string
	androidRepos []string
	mavenCache   string
)

// depsFile, in a Go package's directory, lists Maven coordinates the
// package's Java code needs, one per line: an app importing the package
// gets them.
const depsFile = "android_dependencies.txt"

// pkgManifestFile, in a Go package's directory, is a manifest whose
// permissions and <application> components are merged into the app's,
// as a library's are: the services the package's Java code needs.
const pkgManifestFile = "android_manifest.xml"

// pkgManifests are the manifests of the packages the app imports.
var pkgManifests []*xmlNode

// readPkgManifest reads a package directory's pkgManifestFile.
func readPkgManifest(dir string) (*xmlNode, error) {
	b, err := os.ReadFile(filepath.Join(dir, pkgManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n, err := parseXML(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, pkgManifestFile), err)
	}
	return n, nil
}

// readDeps reads the coordinates in a package directory's depsFile.
func readDeps(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, depsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var deps []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			deps = append(deps, l)
		}
	}
	return deps, nil
}

// resolveLibs returns androidLibs with deps and their dependencies
// downloaded.
func resolveLibs(deps []string) ([]string, error) {
	libs := append([]string{}, androidLibs...)
	if len(deps) == 0 {
		return libs, nil
	}
	var roots []maven.Coord
	seen := map[string]bool{}
	for _, d := range deps {
		if seen[d] {
			continue
		}
		seen[d] = true
		c, err := maven.Parse(d)
		if err != nil {
			return nil, err
		}
		roots = append(roots, c)
	}
	cache := mavenCache
	if cache == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		cache = filepath.Join(dir, "giode", "maven")
	}
	repos := androidRepos
	if len(repos) == 0 {
		repos = maven.DefaultRepos
	}
	r := &maven.Resolver{Repos: repos, Cache: cache}
	if *printCommands {
		r.Log = func(s string) { progress.Logf("maven: %s", s) }
	}
	done := progress.Begin("Resolving Android libraries")
	arts, err := r.Resolve(roots)
	done(err)
	if err != nil {
		return nil, err
	}
	for _, a := range arts {
		libs = append(libs, a.Path)
	}
	return libs, nil
}

// aarLib is an unpacked library.
type aarLib struct {
	pkg      string
	resZip   string
	manifest *xmlNode
	jars     []string
	jni      map[string][]string
	assets   string
}

// unpackLibs unpacks paths into dir, compiling AAR resources with
// aapt2, and returns the AARs and every JAR to dex.
func unpackLibs(paths []string, dir, aapt2 string) ([]aarLib, []string, error) {
	var libs []aarLib
	var jars []string
	for i, p := range paths {
		if strings.HasSuffix(p, ".jar") {
			jars = append(jars, p)
			continue
		}
		d := filepath.Join(dir, "aar"+strconv.Itoa(i))
		if err := unzipTo(p, d); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		lib := aarLib{jni: map[string][]string{}}
		if b, err := os.ReadFile(filepath.Join(d, "AndroidManifest.xml")); err == nil {
			n, err := parseXML(b)
			if err != nil {
				return nil, nil, fmt.Errorf("%s's manifest: %w", filepath.Base(p), err)
			}
			lib.manifest = n
			lib.pkg = n.attr("package")
		}
		if _, err := os.Stat(filepath.Join(d, "classes.jar")); err == nil {
			lib.jars = append(lib.jars, filepath.Join(d, "classes.jar"))
		}
		more, _ := filepath.Glob(filepath.Join(d, "libs", "*.jar"))
		lib.jars = append(lib.jars, more...)
		jars = append(jars, lib.jars...)
		if hasFiles(filepath.Join(d, "res")) {
			lib.resZip = filepath.Join(dir, "aar"+strconv.Itoa(i)+"-res.zip")
			if _, err := runCmd(exec.Command(aapt2, "compile", "--dir", filepath.Join(d, "res"), "-o", lib.resZip)); err != nil {
				return nil, nil, fmt.Errorf("%s's resources: %w", filepath.Base(p), err)
			}
		}
		sos, _ := filepath.Glob(filepath.Join(d, "jni", "*", "*.so"))
		for _, so := range sos {
			abi := filepath.Base(filepath.Dir(so))
			lib.jni[abi] = append(lib.jni[abi], so)
		}
		if hasFiles(filepath.Join(d, "assets")) {
			lib.assets = filepath.Join(d, "assets")
		}
		libs = append(libs, lib)
	}
	return libs, jars, nil
}

// libResources returns the libraries' compiled resources and packages,
// for aapt2 link.
func libResources(libs []aarLib) (zips, pkgs []string) {
	for _, l := range libs {
		if l.resZip != "" {
			zips = append(zips, l.resZip)
			if l.pkg != "" {
				pkgs = append(pkgs, l.pkg)
			}
		}
	}
	return zips, pkgs
}

// mergedManifest is what the libraries add to the app's manifest.
type mergedManifest struct {
	permissions []string
	// application and manifest are XML for inside <application> and
	// <manifest>.
	application, manifest string
	// minSDK is the highest minimum SDK a library asks for.
	minSDK int
}

var toolsAttr = regexp.MustCompile(`\s+tools:[A-Za-z]+="[^"]*"`)

// mergeManifests merges the libraries' manifests: permissions, the
// components in <application> (one per android:name, their children
// merged, as Gradle does), and <queries>. ${applicationId} becomes
// appID.
func mergeManifests(libs []aarLib, appID string, appXML string) mergedManifest {
	m := mergedManifest{application: appXML}
	if len(libs) == 0 {
		return m
	}
	seenPerm := map[string]bool{}
	app := &xmlNode{name: "application"}
	// The app's own components win over libraries'; XML that doesn't
	// parse is kept as it is, ahead of theirs.
	raw := ""
	if appXML != "" {
		if n, err := parseXML([]byte(`<application xmlns:android="http://schemas.android.com/apk/res/android" xmlns:tools="http://schemas.android.com/tools">` + appXML + "</application>")); err == nil {
			app = n
		} else {
			raw = appXML + "\n"
		}
	}
	var queries []*xmlNode
	for _, l := range libs {
		if l.manifest == nil {
			continue
		}
		for _, c := range l.manifest.children {
			switch c.name {
			case "uses-permission", "uses-permission-sdk-23":
				if p := c.attr("android:name"); p != "" && !seenPerm[p] {
					seenPerm[p] = true
					m.permissions = append(m.permissions, p)
				}
			case "uses-sdk":
				if v, err := strconv.Atoi(c.attr("android:minSdkVersion")); err == nil && v > m.minSDK {
					m.minSDK = v
				}
			case "queries":
				queries = append(queries, c.children...)
			case "application":
				for _, comp := range c.children {
					mergeInto(app, comp)
				}
			}
		}
	}
	var b strings.Builder
	for _, c := range app.children {
		c.write(&b, "\t\t")
	}
	m.application = raw + strings.ReplaceAll(b.String(), "${applicationId}", appID)
	m.application = toolsAttr.ReplaceAllString(m.application, "")
	if len(queries) > 0 {
		var q strings.Builder
		q.WriteString("\t<queries>\n")
		for _, c := range queries {
			c.write(&q, "\t\t")
		}
		q.WriteString("\t</queries>\n")
		m.manifest = toolsAttr.ReplaceAllString(strings.ReplaceAll(q.String(), "${applicationId}", appID), "")
	}
	return m
}

// mergeInto adds n to parent, merging it into a child with the same
// tag and android:name.
func mergeInto(parent, n *xmlNode) {
	name := n.attr("android:name")
	if name != "" {
		for _, c := range parent.children {
			if c.name == n.name && c.attr("android:name") == name {
				for _, gc := range n.children {
					mergeInto(c, gc)
				}
				return
			}
		}
	} else if n.name == "intent-filter" {
		// Identical intent filters merge.
		for _, c := range parent.children {
			if c.name == n.name && c.String() == n.String() {
				return
			}
		}
	}
	parent.children = append(parent.children, n)
}

// xmlNode is an element of a small XML tree, enough to merge manifests.
type xmlNode struct {
	name     string
	attrs    []xml.Attr
	children []*xmlNode
}

func (n *xmlNode) attr(name string) string {
	for _, a := range n.attrs {
		if attrName(a.Name) == name {
			return a.Value
		}
	}
	return ""
}

// attrName prefixes the Android namespace as android: and tools' as
// tools:.
func attrName(n xml.Name) string {
	switch n.Space {
	case "":
		return n.Local
	case "http://schemas.android.com/apk/res/android", "android":
		return "android:" + n.Local
	case "http://schemas.android.com/tools", "tools":
		return "tools:" + n.Local
	}
	return n.Local
}

func parseXML(b []byte) (*xmlNode, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	var stack []*xmlNode
	var root *xmlNode
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xmlNode{name: t.Name.Local}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				n.attrs = append(n.attrs, a)
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
	if root == nil {
		return nil, fmt.Errorf("no root element")
	}
	return root, nil
}

func (n *xmlNode) write(b *strings.Builder, indent string) {
	b.WriteString(indent + "<" + n.name)
	attrs := append([]xml.Attr{}, n.attrs...)
	sort.SliceStable(attrs, func(i, j int) bool {
		return attrName(attrs[i].Name) == "android:name" && attrName(attrs[j].Name) != "android:name"
	})
	for _, a := range attrs {
		var v bytes.Buffer
		xml.EscapeText(&v, []byte(a.Value))
		b.WriteString(" " + attrName(a.Name) + "=\"" + strings.ReplaceAll(v.String(), `"`, "&quot;") + "\"")
	}
	if len(n.children) == 0 {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	for _, c := range n.children {
		c.write(b, indent+"\t")
	}
	b.WriteString(indent + "</" + n.name + ">\n")
}

func (n *xmlNode) String() string {
	var b strings.Builder
	n.write(&b, "")
	return b.String()
}

func hasFiles(dir string) bool {
	found := false
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func unzipTo(src, dir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		name := filepath.FromSlash(f.Name)
		if strings.Contains(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("bad path %q", f.Name)
		}
		p := filepath.Join(dir, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(p)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// compileR compiles the R.java files aapt2 generated in genDir into
// outDir and returns the classes.
func compileR(genDir, outDir string) ([]string, error) {
	var srcs []string
	filepath.Walk(genDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".java") {
			srcs = append(srcs, p)
		}
		return nil
	})
	if len(srcs) == 0 {
		return nil, nil
	}
	javac, err := findJavaC()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	// R classes need nothing beyond java.lang: the JDK's own classes do.
	cmd := exec.Command(javac, "-target", "1.8", "-source", "1.8", "-nowarn", "-Xlint:-options", "-d", outDir)
	sources, err := argFile(filepath.Join(filepath.Dir(outDir), "rclasses-sources.txt"), srcs)
	if err != nil {
		return nil, err
	}
	cmd.Args = append(cmd.Args, sources)
	if _, err := runCmd(cmd); err != nil {
		return nil, err
	}
	var classes []string
	filepath.Walk(outDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".class") {
			classes = append(classes, p)
		}
		return nil
	})
	return classes, nil
}
