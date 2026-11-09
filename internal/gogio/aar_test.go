package gogio

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeZip writes files (name → content) to a zip at path.
func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		e, _ := w.Create(name)
		e.Write([]byte(content))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// zipBytes is a zip (a jar) holding files, as a string to put in another.
func zipBytes(t *testing.T, files map[string]string) string {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, content := range files {
		e, _ := w.Create(name)
		e.Write([]byte(content))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func zipNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	names := map[string]bool{}
	for _, f := range r.File {
		names[f.Name] = true
	}
	return names
}

const messagingManifest = `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android" xmlns:tools="http://schemas.android.com/tools" package="com.google.firebase.messaging">
    <uses-sdk android:minSdkVersion="21"/>
    <uses-permission android:name="android.permission.INTERNET"/>
    <uses-permission android:name="com.google.android.c2dm.permission.RECEIVE"/>
    <application>
        <service android:name="com.google.firebase.messaging.FirebaseMessagingService" android:exported="false" tools:ignore="X">
            <intent-filter android:priority="-500">
                <action android:name="com.google.firebase.MESSAGING_EVENT"/>
            </intent-filter>
        </service>
        <service android:name="com.google.firebase.components.ComponentDiscoveryService" android:exported="false">
            <meta-data android:name="com.google.firebase.components:com.google.firebase.messaging.FirebaseMessagingRegistrar" android:value="com.google.firebase.components.ComponentRegistrar"/>
        </service>
        <provider android:name="com.google.firebase.provider.FirebaseInitProvider" android:authorities="${applicationId}.firebaseinitprovider" android:exported="false"/>
    </application>
</manifest>`

const installationsManifest = `<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="com.google.firebase.installations">
    <uses-sdk android:minSdkVersion="19"/>
    <uses-permission android:name="android.permission.INTERNET"/>
    <queries><package android:name="com.android.vending"/></queries>
    <application>
        <service android:name="com.google.firebase.components.ComponentDiscoveryService" android:exported="false">
            <meta-data android:name="com.google.firebase.components:com.google.firebase.installations.FirebaseInstallationsRegistrar" android:value="com.google.firebase.components.ComponentRegistrar"/>
        </service>
    </application>
</manifest>`

func TestMergeManifests(t *testing.T) {
	parse := func(s string) *xmlNode {
		n, err := parseXML([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	libs := []aarLib{{manifest: parse(messagingManifest)}, {manifest: parse(installationsManifest)}}
	app := `		<service android:name="org.giode.push.PushService" android:exported="false"/>`
	m := mergeManifests(libs, "com.example.app", app)

	if strings.Join(m.permissions, ",") != "android.permission.INTERNET,com.google.android.c2dm.permission.RECEIVE" {
		t.Errorf("permissions %v", m.permissions)
	}
	if m.minSDK != 21 {
		t.Errorf("minSDK %d", m.minSDK)
	}
	a := m.application
	if !strings.Contains(a, `android:authorities="com.example.app.firebaseinitprovider"`) {
		t.Errorf("${applicationId} not replaced:\n%s", a)
	}
	if strings.Contains(a, "tools:") {
		t.Errorf("tools attributes kept:\n%s", a)
	}
	if strings.Index(a, "org.giode.push.PushService") > strings.Index(a, "FirebaseMessagingService") {
		t.Errorf("the app's components should come first:\n%s", a)
	}
	// The two ComponentDiscoveryService declarations merge into one with
	// both registrars.
	if c := strings.Count(a, `android:name="com.google.firebase.components.ComponentDiscoveryService"`); c != 1 {
		t.Errorf("ComponentDiscoveryService declared %d times:\n%s", c, a)
	}
	for _, r := range []string{"FirebaseMessagingRegistrar", "FirebaseInstallationsRegistrar"} {
		if !strings.Contains(a, r) {
			t.Errorf("lacks %s:\n%s", r, a)
		}
	}
	if !strings.Contains(m.manifest, `<package android:name="com.android.vending"/>`) {
		t.Errorf("queries: %s", m.manifest)
	}
	// The merged XML parses.
	if _, err := parseXML([]byte(`<application xmlns:android="http://schemas.android.com/apk/res/android">` + a + `</application>`)); err != nil {
		t.Errorf("merged XML: %v\n%s", err, a)
	}

	// Without libraries, the app's XML is untouched.
	if m := mergeManifests(nil, "x", "<odd"); m.application != "<odd" {
		t.Errorf("no libraries: %q", m.application)
	}
}

func TestReadDeps(t *testing.T) {
	dir := t.TempDir()
	if d, err := readDeps(dir); err != nil || d != nil {
		t.Fatalf("no file: %v %v", d, err)
	}
	os.WriteFile(filepath.Join(dir, depsFile), []byte("# push\ncom.google.firebase:firebase-messaging:24.1.0\n\n  com.android.billingclient:billing:7.1.1 \n"), 0o644)
	d, err := readDeps(dir)
	if err != nil || strings.Join(d, ",") != "com.google.firebase:firebase-messaging:24.1.0,com.android.billingclient:billing:7.1.1" {
		t.Errorf("deps %v %v", d, err)
	}
}

// fakeTools writes aapt2 and d8 scripts that log their arguments and
// make plausible outputs.
func fakeTools(t *testing.T) (dir, log string) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	dir = t.TempDir()
	log = filepath.Join(dir, "log")
	aapt2 := `#!/bin/sh
echo "aapt2 $*" >> ` + log + `
cmd=$1; shift
out=""; java=""; manifest=""
while [ $# -gt 0 ]; do
	case $1 in
	-o) out=$2; shift;;
	--java) java=$2; shift;;
	--manifest) manifest=$2; shift;;
	esac
	shift
done
if [ "$cmd" = compile ]; then echo res > "$out"; exit 0; fi
d=$(mktemp -d); cp "$manifest" "$d/AndroidManifest.xml"; echo r > "$d/resources.arsc"
(cd "$d" && zip -q "$out" AndroidManifest.xml resources.arsc)
if [ -n "$java" ]; then
	mkdir -p "$java/com/example/lib"
	printf 'package com.example.lib;\npublic final class R { public static final class string { public static int hello = 0x7f010000; } }\n' > "$java/com/example/lib/R.java"
fi
`
	d8 := `#!/bin/sh
echo "d8 $*" >> ` + log + `
while [ $# -gt 0 ]; do
	if [ "$1" = --output ]; then out=$2; fi
	shift
done
echo dex > "$out/classes.dex"; echo dex2 > "$out/classes2.dex"
`
	os.WriteFile(filepath.Join(dir, "aapt2"), []byte(aapt2), 0o755)
	os.WriteFile(filepath.Join(dir, "d8"), []byte(d8), 0o755)
	return dir, log
}

// TestExeAndroidLibs builds an APK's contents from an AAR and a JAR
// with fake tools, checking what's linked, dexed and packaged.
func TestExeAndroidLibs(t *testing.T) {
	if _, err := findJavaC(); err != nil {
		t.Skip("no javac")
	}
	tools, log := fakeTools(t)
	if err := runCmdCheck("zip", "-v"); err != nil {
		t.Skip("no zip")
	}
	stubs := filepath.Join(t.TempDir(), "android.jar")
	writeZip(t, stubs, map[string]string{"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\n"})

	libDir := t.TempDir()
	aar := filepath.Join(libDir, "lib.aar")
	writeZip(t, aar, map[string]string{
		"AndroidManifest.xml":        `<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="com.example.lib"><uses-permission android:name="android.permission.VIBRATE"/><application><activity android:name="com.example.lib.Screen"/></application></manifest>`,
		"classes.jar":                zipBytes(t, map[string]string{"com/example/lib/Screen.class": "x"}),
		"libs/extra.jar":             zipBytes(t, map[string]string{"com/example/extra/Util.class": "x"}),
		"res/values/strings.xml":     `<resources><string name="hello">Hi</string></resources>`,
		"jni/arm64-v8a/libnative.so": "so",
		"jni/x86/libnative.so":       "so",
		"assets/model/data.bin":      "data",
		"proguard.txt":               "",
		"R.txt":                      "",
	})
	jar := filepath.Join(libDir, "plain.jar")
	writeZip(t, jar, map[string]string{"a/B.class": "x"})

	tmp := t.TempDir()
	for _, a := range []string{"arm64"} {
		d := filepath.Join(tmp, "jni", allArchs[a].jniArch)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "libgio.so"), []byte("go"), 0o644)
	}
	os.MkdirAll(filepath.Join(tmp, "classes", "org", "gioui"), 0o755)
	os.WriteFile(filepath.Join(tmp, "classes", "org", "gioui", "GioView.class"), []byte("x"), 0o644)

	bi := &buildInfo{appID: "com.example.app", name: "app", archs: []string{"arm64"}, minsdk: 21, version: Semver{1, 0, 0, 1}}
	at := &androidTools{buildtools: tools, androidjar: stubs}
	if err := exeAndroid(tmp, at, bi, nil, []string{aar, jar}, []string{"default"}, false); err != nil {
		t.Fatal(err)
	}

	logb, _ := os.ReadFile(log)
	l := string(logb)
	for _, w := range []string{"--auto-add-overlay", "--extra-packages com.example.lib", "--java " + filepath.Join(tmp, "gen")} {
		if !strings.Contains(l, w) {
			t.Errorf("aapt2 link lacks %q:\n%s", w, l)
		}
	}
	if strings.Index(l, "aapt2 link") > strings.Index(l, "d8 ") {
		t.Errorf("d8 ran before the link made R:\n%s", l)
	}
	var d8 string
	for _, line := range strings.Split(l, "\n") {
		if strings.HasPrefix(line, "d8 ") {
			d8 = line
		}
	}
	// d8 gets one jar with every class, the app's and the libraries'.
	input := filepath.Join(tmp, "dexinput.jar")
	for _, w := range []string{input, "--min-api 21"} {
		if !strings.Contains(d8, w) {
			t.Errorf("d8 lacks %s: %s", w, d8)
		}
	}
	classes := zipNames(t, input)
	for _, c := range []string{"com/example/lib/Screen.class", "com/example/extra/Util.class", "a/B.class", "org/gioui/GioView.class", "com/example/lib/R$string.class"} {
		if !classes[c] {
			t.Errorf("d8's input lacks %s: %v", c, classes)
		}
	}

	names := zipNames(t, filepath.Join(tmp, "app.zip"))
	for _, n := range []string{"AndroidManifest.xml", "classes.dex", "classes2.dex", "lib/arm64-v8a/libgio.so", "lib/arm64-v8a/libnative.so", "assets/model/data.bin"} {
		if !names[n] {
			t.Errorf("APK lacks %s: %v", n, names)
		}
	}
	if names["lib/x86/libnative.so"] {
		t.Error("packaged an ABI the app wasn't built for")
	}
	manifest, _ := os.ReadFile(filepath.Join(tmp, "AndroidManifest.xml"))
	for _, w := range []string{"android.permission.VIBRATE", `<activity android:name="com.example.lib.Screen"/>`} {
		if !strings.Contains(string(manifest), w) {
			t.Errorf("manifest lacks %s:\n%s", w, manifest)
		}
	}
}

func runCmdCheck(name string, args ...string) error {
	_, err := runCmd(exec.Command(name, args...))
	return err
}

func TestPkgManifest(t *testing.T) {
	dir := t.TempDir()
	if n, err := readPkgManifest(dir); n != nil || err != nil {
		t.Fatalf("none: %v %v", n, err)
	}
	os.WriteFile(filepath.Join(dir, pkgManifestFile), []byte(`<manifest xmlns:android="http://schemas.android.com/apk/res/android">
	<uses-permission android:name="android.permission.WAKE_LOCK"/>
	<application>
		<service android:name="org.giode.push.PushService" android:exported="false">
			<intent-filter><action android:name="com.google.firebase.MESSAGING_EVENT"/></intent-filter>
		</service>
	</application>
</manifest>`), 0o644)
	n, err := readPkgManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := mergeManifests([]aarLib{{manifest: n}}, "com.example.app", "")
	if len(m.permissions) != 1 || !strings.Contains(m.application, `<action android:name="com.google.firebase.MESSAGING_EVENT"/>`) {
		t.Errorf("%v\n%s", m.permissions, m.application)
	}
}
