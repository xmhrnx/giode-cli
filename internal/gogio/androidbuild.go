// SPDX-License-Identifier: Unlicense OR MIT
//
// Derived from gioui.org/cmd/gogio (v0.10.3); see LICENSE in this directory.

package gogio

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/template"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
)

type androidTools struct {
	buildtools string
	androidjar string
}

// zip.Writer with a sticky error.
type zipWriter struct {
	err error
	w   *zip.Writer
}

// Writer that saves any errors.
type errWriter struct {
	w   io.Writer
	err *error
}

var exeSuffix string

type manifestData struct {
	AppID          string
	Version        Semver
	MinSDK         int
	TargetSDK      int
	Permissions    []string
	Features       []string
	IconSnip       string
	AppName        string
	Schemes        []string
	PackageQueries []string
	// ManifestXML and ApplicationXML come from Extras.
	ManifestXML    string
	ApplicationXML string
	// ApplicationAttrs are extra <application> attributes
	// (Extras.AndroidApplicationAttrs).
	ApplicationAttrs string
	// ActivityXML goes inside the activity (Extras.AndroidActivity).
	ActivityXML string
}

const (
	themes = `<?xml version="1.0" encoding="utf-8"?>
<resources>
	<style name="Theme.GioApp" parent="android:style/Theme.NoTitleBar">
		<item name="android:windowBackground">@android:color/white</item>
	</style>
</resources>`
	themesV21 = `<?xml version="1.0" encoding="utf-8"?>
<resources>
	<style name="Theme.GioApp" parent="android:style/Theme.NoTitleBar">
		<item name="android:windowBackground">@android:color/white</item>

		<item name="android:windowDrawsSystemBarBackgrounds">true</item>
		<item name="android:navigationBarColor">#40000000</item>
		<item name="android:statusBarColor">#40000000</item>
	</style>
</resources>`
)

func init() {
	if runtime.GOOS == "windows" {
		exeSuffix = ".exe"
	}
}

func buildAndroid(tmpDir string, bi *buildInfo) error {
	sdk := os.Getenv("ANDROID_HOME")
	if sdk == "" {
		return errors.New("please set ANDROID_HOME to the Android SDK path")
	}
	if _, err := os.Stat(sdk); err != nil {
		return err
	}
	platform, err := latestPlatform(sdk)
	if err != nil {
		return err
	}
	buildtools, err := latestTools(sdk)
	if err != nil {
		return err
	}

	tools := &androidTools{
		buildtools: buildtools,
		androidjar: filepath.Join(platform, "android.jar"),
	}
	perms := []string{"default"}
	const permPref = "gioui.org/app/permission/"
	cfg := &packages.Config{
		Mode: packages.NeedName +
			packages.NeedFiles +
			packages.NeedImports +
			packages.NeedDeps,
		Env: append(
			os.Environ(),
			"GOOS=android",
			"CGO_ENABLED=1",
		),
	}
	pkgs, err := packages.Load(cfg, bi.pkgPath)
	if err != nil {
		return err
	}
	var extraJars []string
	deps := append([]string{}, androidDeps...)
	pkgManifests = nil
	defer func() { pkgManifests = nil }()
	visitedPkgs := make(map[string]bool)
	var visitPkg func(*packages.Package) error
	visitPkg = func(p *packages.Package) error {
		if len(p.GoFiles) == 0 {
			return nil
		}
		dir := filepath.Dir(p.GoFiles[0])
		jars, err := filepath.Glob(filepath.Join(dir, "*.jar"))
		if err != nil {
			return err
		}
		extraJars = append(extraJars, jars...)
		d, err := readDeps(dir)
		if err != nil {
			return err
		}
		deps = append(deps, d...)
		m, err := readPkgManifest(dir)
		if err != nil {
			return err
		}
		if m != nil {
			pkgManifests = append(pkgManifests, m)
		}
		switch {
		case p.PkgPath == "net":
			perms = append(perms, "network")
		case strings.HasPrefix(p.PkgPath, permPref):
			perms = append(perms, p.PkgPath[len(permPref):])
		}

		for _, imp := range p.Imports {
			if !visitedPkgs[imp.ID] {
				visitPkg(imp)
				visitedPkgs[imp.ID] = true
			}
		}
		return nil
	}
	if err := visitPkg(pkgs[0]); err != nil {
		return err
	}

	for _, d := range deps {
		if strings.HasPrefix(d, "com.google.firebase:") && !strings.Contains(extras.AndroidValues, `"google_app_id"`) {
			return fmt.Errorf("%s needs the app's Firebase configuration: download google-services.json from the Firebase console and set android.google_services in giode.toml", d)
		}
	}
	libs, err := resolveLibs(deps)
	if err != nil {
		return err
	}
	if err := compileAndroid(tmpDir, tools, bi); err != nil {
		return err
	}
	switch *buildMode {
	case "archive":
		return archiveAndroid(tmpDir, bi, perms)
	case "exe":
		file := *destPath
		if file == "" {
			file = fmt.Sprintf("%s.apk", bi.name)
		}

		isBundle := false
		switch filepath.Ext(file) {
		case ".apk":
		case ".aab":
			isBundle = true
		default:
			return fmt.Errorf("the specified output %q does not end in '.apk' or '.aab'", file)
		}

		if err := exeAndroid(tmpDir, tools, bi, extraJars, libs, perms, isBundle); err != nil {
			return err
		}
		if isBundle {
			return signAAB(tmpDir, file, tools, bi)
		}
		return signAPK(tmpDir, file, tools, bi)
	default:
		panic("unreachable")
	}
}

func compileAndroid(tmpDir string, tools *androidTools, bi *buildInfo) (err error) {
	androidHome := os.Getenv("ANDROID_HOME")
	if androidHome == "" {
		return errors.New("ANDROID_HOME is not set. Please point it to the root of the Android SDK")
	}
	javac, err := findJavaC()
	if err != nil {
		return fmt.Errorf("could not find javac: %v", err)
	}
	ndkRoot, err := findNDK(androidHome)
	if err != nil {
		return err
	}
	minSDK := max(bi.minsdk, 17)
	tcRoot := filepath.Join(ndkRoot, "toolchains", "llvm", "prebuilt", archNDK())
	var builds errgroup.Group
	for _, a := range bi.archs {
		arch := allArchs[a]
		clang, err := latestCompiler(tcRoot, a, minSDK)
		if err != nil {
			return fmt.Errorf("%s. Please make sure you have NDK >= r19c installed. Use the command `sdkmanager ndk-bundle` to install it.", err)
		}
		if runtime.GOOS == "windows" {
			// Because of https://github.com/android-ndk/ndk/issues/920,
			// we need NDK r19c, not just r19b. Check for the presence of
			// clang++.cmd which is only available in r19c.
			clangpp := clang + "++.cmd"
			if _, err := os.Stat(clangpp); err != nil {
				return fmt.Errorf("NDK version r19b detected, but >= r19c is required. Use the command `sdkmanager ndk-bundle` to install it")
			}
		}
		archDir := filepath.Join(tmpDir, "jni", arch.jniArch)
		if err := os.MkdirAll(archDir, 0o755); err != nil {
			return fmt.Errorf("failed to create %q: %v", archDir, err)
		}
		libFile := filepath.Join(archDir, "libgio.so")
		// giode: with a symbols directory, build with symbols, keep a
		// copy for the Play Console and strip the packaged library.
		strip := "-w -s "
		if symbolsDir != "" {
			strip = ""
		}
		cmd := goCommand("build",
			"-ldflags="+strip+"-extldflags \"-Wl,-z,max-page-size=65536\" "+bi.ldflags,
			"-buildmode=c-shared",
			"-tags", bi.tags,
			"-o", libFile,
			bi.pkgPath,
		)
		cmd.Env = append(
			os.Environ(),
			"GOOS=android",
			"GOARCH="+a,
			"GOARM=7", // Avoid softfloat.
			"CGO_ENABLED=1",
			"CC="+clang,
			"CXX="+clang+"++",
		)
		jniArch := arch.jniArch
		builds.Go(func() error {
			if _, err := runCmd(cmd); err != nil {
				return err
			}
			if symbolsDir == "" {
				return nil
			}
			return splitSymbols(tcRoot, libFile, filepath.Join(symbolsDir, jniArch, "libgio.so"))
		})
	}
	appDir, err := runCmd(goCommand("list", "-tags", bi.tags, "-f", "{{.Dir}}", "gioui.org/app/"))
	if err != nil {
		return err
	}
	javaFiles, err := filepath.Glob(filepath.Join(appDir, "*.java"))
	if err != nil {
		return err
	}
	if len(javaFiles) == 0 {
		return fmt.Errorf("the gioui.org/app package contains no .java files (gioui.org module too old?)")
	}
	javaFiles = append(javaFiles, extras.AndroidJava...)
	if len(javaFiles) > 0 {
		classes := filepath.Join(tmpDir, "classes")
		if err := os.MkdirAll(classes, 0o755); err != nil {
			return err
		}
		javac := exec.Command(
			javac,
			"-target", "1.8",
			"-source", "1.8",
			"-sourcepath", appDir,
			"-bootclasspath", tools.androidjar,
			"-d", classes,
		)
		javac.Args = append(javac.Args, javaFiles...)
		builds.Go(func() error {
			_, err := runCmd(javac)
			return err
		})
	}
	return builds.Wait()
}

func archiveAndroid(tmpDir string, bi *buildInfo, perms []string) (err error) {
	aarFile := *destPath
	if aarFile == "" {
		aarFile = fmt.Sprintf("%s.aar", bi.name)
	}
	if filepath.Ext(aarFile) != ".aar" {
		return fmt.Errorf("the specified output %q does not end in '.aar'", aarFile)
	}
	aar, err := os.Create(aarFile)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := aar.Close(); err == nil {
			err = cerr
		}
	}()
	aarw := newZipWriter(aar)
	defer aarw.Close()
	aarw.Create("R.txt")
	themesXML := aarw.Create("res/values/themes.xml")
	themesXML.Write([]byte(themes))
	themesXML21 := aarw.Create("res/values-v21/themes.xml")
	themesXML21.Write([]byte(themesV21))
	permissions, features := getPermissions(perms)
	permissions = addPermissions(permissions, extras.AndroidPermissions)
	// Disable input emulation on ChromeOS.
	manifest := aarw.Create("AndroidManifest.xml")
	manifestSrc := manifestData{
		AppID:          bi.appID,
		MinSDK:         bi.minsdk,
		Permissions:    permissions,
		Features:       features,
		ManifestXML:    extras.AndroidManifest,
		ApplicationXML: extras.AndroidApplication,
	}
	tmpl, err := template.New("manifest").Parse(
		`<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="{{.AppID}}">
        <uses-sdk android:minSdkVersion="{{.MinSDK}}"/>
{{range .Permissions}}	<uses-permission android:name="{{.}}"/>
{{end}}{{range .Features}}	<uses-feature android:{{.}} android:required="false"/>
{{end}}{{.ManifestXML}}
{{if .ApplicationXML}}	<application>
{{.ApplicationXML}}
	</application>
{{end}}</manifest>
`)
	if err != nil {
		panic(err)
	}
	err = tmpl.Execute(manifest, manifestSrc)
	proguard := aarw.Create("proguard.txt")
	proguard.Write([]byte(`-keep class org.gioui.** { *; }`))

	for _, a := range bi.archs {
		arch := allArchs[a]
		libFile := filepath.Join("jni", arch.jniArch, "libgio.so")
		aarw.Add(filepath.ToSlash(libFile), filepath.Join(tmpDir, libFile))
	}
	classes := filepath.Join(tmpDir, "classes")
	if _, err := os.Stat(classes); err == nil {
		jarFile := filepath.Join(tmpDir, "classes.jar")
		if err := writeJar(jarFile, classes); err != nil {
			return err
		}
		aarw.Add("classes.jar", jarFile)
	}
	return aarw.Close()
}

func exeAndroid(tmpDir string, tools *androidTools, bi *buildInfo, extraJars, libPaths, perms []string, isBundle bool) (err error) {
	aapt2 := filepath.Join(tools.buildtools, "aapt2")
	libs, libJars, err := unpackLibs(libPaths, filepath.Join(tmpDir, "libs"), aapt2)
	if err != nil {
		return err
	}
	for _, m := range pkgManifests {
		libs = append(libs, aarLib{manifest: m})
	}
	merged := mergeManifests(libs, bi.appID, extras.AndroidApplication)
	classes := filepath.Join(tmpDir, "classes")
	var classFiles []string
	err = filepath.Walk(classes, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".class" {
			classFiles = append(classFiles, path)
		}
		return nil
	})
	classFiles = append(classFiles, extraJars...)
	classFiles = append(classFiles, libJars...)
	dexDir := filepath.Join(tmpDir, "apk")
	if err := os.MkdirAll(dexDir, 0o755); err != nil {
		return err
	}
	minSDK := max(bi.minsdk, 16, merged.minSDK)
	// https://developer.android.com/distribute/best-practices/develop/target-sdk
	targetSDK := 35
	if bi.targetsdk > 0 {
		targetSDK = bi.targetsdk
	}
	if minSDK > targetSDK {
		targetSDK = minSDK
	}
	// Compile resources.
	resDir := filepath.Join(tmpDir, "res")
	valDir := filepath.Join(resDir, "values")
	v21Dir := filepath.Join(resDir, "values-v21")
	v26mipmapDir := filepath.Join(resDir, `mipmap-anydpi-v26`)
	for _, dir := range []string{valDir, v21Dir, v26mipmapDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	iconSnip := ""
	if _, err := os.Stat(bi.iconPath); err == nil {
		// Legacy icons for Android 7 and earlier; the adaptive icon's
		// layers for 8 and later.
		err := buildIcons(resDir, bi.iconPath, []iconVariant{
			{path: filepath.Join("mipmap-hdpi", "ic_launcher.png"), size: 72},
			{path: filepath.Join("mipmap-xhdpi", "ic_launcher.png"), size: 96},
			{path: filepath.Join("mipmap-xxhdpi", "ic_launcher.png"), size: 144},
			{path: filepath.Join("mipmap-xxxhdpi", "ic_launcher.png"), size: 192},
		})
		if err != nil {
			return err
		}
		appIcon, err := decodePNG(bi.iconPath)
		if err != nil {
			return err
		}
		adaptive, err := writeAdaptiveIcon(resDir, appIcon, extras.AndroidIcon)
		if err != nil {
			return err
		}
		err = os.WriteFile(filepath.Join(v26mipmapDir, `ic_launcher.xml`), []byte(adaptive), 0o660)
		if err != nil {
			return err
		}
		iconSnip = `android:icon="@mipmap/ic_launcher"`
	}
	err = os.WriteFile(filepath.Join(valDir, "themes.xml"), []byte(themes), 0o660)
	if err != nil {
		return err
	}
	err = os.WriteFile(filepath.Join(v21Dir, "themes.xml"), []byte(themesV21), 0o660)
	if err != nil {
		return err
	}
	if extras.AndroidValues != "" {
		if err := os.WriteFile(filepath.Join(valDir, "giode_values.xml"), []byte(extras.AndroidValues), 0o660); err != nil {
			return err
		}
	}
	resZip := filepath.Join(tmpDir, "resources.zip")
	_, err = runCmd(exec.Command(
		aapt2,
		"compile",
		"-o", resZip,
		"--dir", resDir))
	if err != nil {
		return err
	}

	// Link APK.
	permissions, features := getPermissions(perms)
	permissions = addPermissions(permissions, extras.AndroidPermissions)
	permissions = addPermissions(permissions, merged.permissions)
	appName := UppercaseName(bi.name)
	manifestSrc := manifestData{
		AppID:            bi.appID,
		Version:          bi.version,
		MinSDK:           minSDK,
		TargetSDK:        targetSDK,
		Permissions:      permissions,
		Features:         features,
		IconSnip:         iconSnip,
		AppName:          appName,
		Schemes:          bi.schemes,
		PackageQueries:   bi.packageQueries,
		ManifestXML:      extras.AndroidManifest + merged.manifest,
		ApplicationXML:   merged.application,
		ApplicationAttrs: extras.AndroidApplicationAttrs,
		ActivityXML:      extras.AndroidActivity,
	}
	manifestXML, err := androidManifest(manifestSrc)
	if err != nil {
		return err
	}
	manifest := filepath.Join(tmpDir, "AndroidManifest.xml")
	if err := os.WriteFile(manifest, manifestXML, 0o660); err != nil {
		return err
	}

	linkAPK := filepath.Join(tmpDir, "link.apk")

	args := []string{
		"link",
		"--manifest", manifest,
		"-I", tools.androidjar,
		"-o", linkAPK,
	}
	if isBundle {
		args = append(args, "--proto-format")
	}
	// giode: libraries' resources are linked under the app's (later
	// overlays win), with R classes generated for their packages.
	libZips, libPkgs := libResources(libs)
	genDir := filepath.Join(tmpDir, "gen")
	if len(libZips) > 0 {
		args = append(args, "--auto-add-overlay", "--java", genDir, "--extra-packages", strings.Join(libPkgs, ":"))
		// The first library is the lowest priority base.
		args = append(args, libZips[len(libZips)-1])
		for i := len(libZips) - 2; i >= 0; i-- {
			args = append(args, "-R", libZips[i])
		}
		args = append(args, "-R", resZip)
	} else {
		args = append(args, resZip)
	}

	if _, err := runCmd(exec.Command(aapt2, args...)); err != nil {
		return err
	}
	if len(libZips) > 0 {
		rClasses, err := compileR(genDir, filepath.Join(tmpDir, "rclasses"))
		if err != nil {
			return err
		}
		classFiles = append(classFiles, rClasses...)
	}

	if len(classFiles) > 0 {
		d8 := exec.Command(
			filepath.Join(tools.buildtools, "d8"),
			"--lib", tools.androidjar,
			"--output", dexDir,
			"--min-api", strconv.Itoa(minSDK),
		)
		d8.Args = append(d8.Args, classFiles...)
		if _, err := runCmd(d8); err != nil {
			major, minor, ok := determineJDKVersion()
			if ok && (major != 1 || minor != 8) {
				return fmt.Errorf("unsupported JDK version %d.%d, expected 1.8\nd8 error: %v", major, minor, err)
			}
			return err
		}
	}

	// The Go standard library archive/zip doesn't support appending to zip
	// files. Copy files from `link.apk` (generated by aapt2) along with classes.dex and
	// the Go libraries to a new `app.zip` file.

	// Load link.apk as zip.
	linkAPKZip, err := zip.OpenReader(linkAPK)
	if err != nil {
		return err
	}
	defer linkAPKZip.Close()

	// Create new "APK".
	unsignedAPK := filepath.Join(tmpDir, "app.zip")
	unsignedAPKFile, err := os.Create(unsignedAPK)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := unsignedAPKFile.Close(); err == nil {
			err = cerr
		}
	}()
	unsignedAPKZip := zip.NewWriter(unsignedAPKFile)
	defer unsignedAPKZip.Close()

	// Copy files from linkAPK to unsignedAPK.
	for _, f := range linkAPKZip.File {
		header := zip.FileHeader{
			Name:   f.FileHeader.Name,
			Method: f.FileHeader.Method,
		}

		if isBundle {
			// AAB have pre-defined folders.
			switch header.Name {
			case "AndroidManifest.xml":
				header.Name = "manifest/AndroidManifest.xml"
			}
		}

		w, err := unsignedAPKZip.CreateHeader(&header)
		if err != nil {
			return err
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, r); err != nil {
			return err
		}
	}

	// Append new files (that doesn't exists inside the link.apk).
	appendToZip := func(path string, file string) error {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		w, err := unsignedAPKZip.CreateHeader(&zip.FileHeader{
			Name:   filepath.ToSlash(path),
			Method: zip.Deflate,
		})
		if err != nil {
			return err
		}
		_, err = io.Copy(w, f)
		return err
	}

	// Append Go binaries (libgio.so).
	for _, a := range bi.archs {
		arch := allArchs[a]
		libFile := filepath.Join(arch.jniArch, "libgio.so")
		if err := appendToZip(filepath.Join("lib", libFile), filepath.Join(tmpDir, "jni", libFile)); err != nil {
			return err
		}
	}

	// Append classes.dex, and the classes2.dex... of a multidex app.
	if len(classFiles) > 0 {
		dexes, err := filepath.Glob(filepath.Join(dexDir, "classes*.dex"))
		if err != nil {
			return err
		}
		for _, dex := range dexes {
			classesFolder := filepath.Base(dex)
			if isBundle {
				classesFolder = "dex/" + classesFolder
			}
			if err := appendToZip(classesFolder, dex); err != nil {
				return err
			}
		}
	}

	// giode: libraries' native libraries, for the app's ABIs, and
	// assets.
	abis := map[string]bool{}
	for _, a := range bi.archs {
		abis[allArchs[a].jniArch] = true
	}
	added := map[string]bool{}
	for _, l := range libs {
		for abi, sos := range l.jni {
			if !abis[abi] {
				continue
			}
			for _, so := range sos {
				name := "lib/" + abi + "/" + filepath.Base(so)
				if added[name] {
					continue
				}
				added[name] = true
				if err := appendToZip(name, so); err != nil {
					return err
				}
			}
		}
		if l.assets == "" {
			continue
		}
		err := filepath.Walk(l.assets, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(l.assets, p)
			name := "assets/" + filepath.ToSlash(rel)
			if added[name] {
				return nil
			}
			added[name] = true
			return appendToZip(name, p)
		})
		if err != nil {
			return err
		}
	}

	return unsignedAPKZip.Close()
}

func determineJDKVersion() (int, int, bool) {
	path, err := findJavaC()
	if err != nil {
		return 0, 0, false
	}
	java := exec.Command(filepath.Join(filepath.Dir(path), "java"), "-version")
	out, err := java.CombinedOutput()
	if err != nil {
		return 0, 0, false
	}
	var vendor string
	var major, minor int
	_, err = fmt.Sscanf(string(out), "%s version \"%d.%d", &vendor, &major, &minor)
	return major, minor, err == nil
}

func signAPK(tmpDir string, apkFile string, tools *androidTools, bi *buildInfo) error {
	if err := zipalign(tools, filepath.Join(tmpDir, "app.zip"), apkFile); err != nil {
		return err
	}

	if bi.key == "" {
		if err := defaultAndroidKeystore(tmpDir, bi); err != nil {
			return err
		}
	}

	_, err := runCmd(exec.Command(
		filepath.Join(tools.buildtools, "apksigner"),
		"sign",
		"--ks-pass", "pass:"+bi.password,
		"--ks", bi.key,
		apkFile,
	))

	return err
}

func signAAB(tmpDir string, aabFile string, tools *androidTools, bi *buildInfo) error {
	bundletool, err := findBundletool(tools.buildtools)
	if err != nil {
		return err
	}

	_, err = runCmd(exec.Command(
		"java",
		"-jar", bundletool,
		"build-bundle",
		"--modules="+filepath.Join(tmpDir, "app.zip"),
		"--output="+filepath.Join(tmpDir, "app.aab"),
	))
	if err != nil {
		return err
	}

	if err := zipalign(tools, filepath.Join(tmpDir, "app.aab"), aabFile); err != nil {
		return err
	}

	if bi.key == "" {
		if err := defaultAndroidKeystore(tmpDir, bi); err != nil {
			return err
		}
	}

	keytoolList, err := runCmd(exec.Command(
		"keytool",
		"-keystore", bi.key,
		"-list",
		"-keypass", bi.password,
		"-v",
	))
	if err != nil {
		return err
	}

	var alias string
	for t := range strings.SplitSeq(keytoolList, "\n") {
		if i, _ := fmt.Sscanf(t, "Alias name: %s", &alias); i > 0 {
			break
		}
	}

	_, err = runCmd(exec.Command(
		filepath.Join("jarsigner"),
		"-sigalg", "SHA256withRSA",
		"-digestalg", "SHA-256",
		"-keystore", bi.key,
		"-storepass", bi.password,
		aabFile,
		strings.TrimSpace(alias),
	))

	return err
}

func zipalign(tools *androidTools, input, output string) error {
	_, err := runCmd(exec.Command(
		filepath.Join(tools.buildtools, "zipalign"),
		"-f",
		"4", // 32-bit alignment.
		input,
		output,
	))
	return err
}

func defaultAndroidKeystore(tmpDir string, bi *buildInfo) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Use debug.keystore, if exists.
	bi.key = filepath.Join(home, ".android", "debug.keystore")
	bi.password = "android"
	if _, err := os.Stat(bi.key); err == nil {
		return nil
	}

	// Generate new key.
	bi.key = filepath.Join(tmpDir, "sign.keystore")
	keytool, err := findKeytool()
	if err != nil {
		return err
	}
	_, err = runCmd(exec.Command(
		keytool,
		"-genkey",
		"-keystore", bi.key,
		"-storepass", bi.password,
		"-alias", "android",
		"-keyalg", "RSA", "-keysize", "2048",
		"-validity", "10000",
		"-noprompt",
		"-dname", "CN=android",
	))
	return err
}

func findNDK(androidHome string) (string, error) {
	ndks, err := filepath.Glob(filepath.Join(androidHome, "ndk", "*"))
	if err != nil {
		return "", err
	}
	if bestNDK, found := latestVersionPath(ndks); found {
		return bestNDK, nil
	}
	// The old NDK path was $ANDROID_HOME/ndk-bundle.
	ndkBundle := filepath.Join(androidHome, "ndk-bundle")
	if _, err := os.Stat(ndkBundle); err == nil {
		return ndkBundle, nil
	}
	// Certain non-standard NDK isntallations set the $ANDROID_NDK_ROOT
	// environment variable
	if ndkBundle, ok := os.LookupEnv("ANDROID_NDK_ROOT"); ok {
		if _, err := os.Stat(ndkBundle); err == nil {
			return ndkBundle, nil
		}
	}

	return "", fmt.Errorf("no NDK found in $ANDROID_HOME (%s). Set $ANDROID_NDK_ROOT or use `sdkmanager ndk-bundle` to install the NDK", androidHome)
}

func findKeytool() (string, error) {
	javaHome := os.Getenv("JAVA_HOME")
	if javaHome == "" {
		return exec.LookPath("keytool")
	}

	// bin, instead of "jre". "jre" was for older JVM it seems.
	keytool := filepath.Join(javaHome, "bin", "keytool"+exeSuffix)
	if _, err := os.Stat(keytool); err != nil {
		return "", err
	}
	return keytool, nil
}

func findJavaC() (string, error) {
	javaHome := os.Getenv("JAVA_HOME")
	if javaHome == "" {
		return exec.LookPath("javac")
	}
	javac := filepath.Join(javaHome, "bin", "javac"+exeSuffix)
	if _, err := os.Stat(javac); err != nil {
		return "", err
	}
	return javac, nil
}

func writeJar(jarFile, dir string) (err error) {
	jar, err := os.Create(jarFile)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := jar.Close(); err == nil {
			err = cerr
		}
	}()
	jarw := newZipWriter(jar)
	const manifestHeader = `Manifest-Version: 1.0
Created-By: 1.0 (Go)

`
	jarw.Create("META-INF/MANIFEST.MF").Write([]byte(manifestHeader))
	err = filepath.Walk(dir, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if f.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".class" {
			rel := filepath.ToSlash(path[len(dir)+1:])
			jarw.Add(rel, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return jarw.Close()
}

func archNDK() string {
	var arch string
	switch runtime.GOARCH {
	case "386":
		arch = "x86"
	case "amd64":
		arch = "x86_64"
	case "arm64":
		if runtime.GOOS == "darwin" {
			// Workaround for arm64 macOS. This will keep working until
			// Apple deprecates Rosetta 2.
			arch = "x86_64"
		} else {
			panic("unsupported GOARCH: " + runtime.GOARCH)
		}
	default:
		panic("unsupported GOARCH: " + runtime.GOARCH)
	}
	return runtime.GOOS + "-" + arch
}

// addPermissions appends the extra permissions not already in ps.
func addPermissions(ps, extra []string) []string {
	seen := make(map[string]bool)
	for _, p := range ps {
		seen[p] = true
	}
	for _, p := range extra {
		if !seen[p] {
			ps = append(ps, p)
			seen[p] = true
		}
	}
	return ps
}

func getPermissions(ps []string) ([]string, []string) {
	var permissions, features []string
	seenPermissions := make(map[string]bool)
	seenFeatures := make(map[string]bool)
	for _, perm := range ps {
		for _, x := range AndroidPermissions[perm] {
			if !seenPermissions[x] {
				permissions = append(permissions, x)
				seenPermissions[x] = true
			}
		}
		for _, x := range AndroidFeatures[perm] {
			if !seenFeatures[x] {
				features = append(features, x)
				seenFeatures[x] = true
			}
		}
	}
	return permissions, features
}

func latestPlatform(sdk string) (string, error) {
	allPlats, err := filepath.Glob(filepath.Join(sdk, "platforms", "android-*"))
	if err != nil {
		return "", err
	}
	var bestVer int
	var bestPlat string
	for _, platform := range allPlats {
		_, name := filepath.Split(platform)
		// The glob above guarantees the "android-" prefix.
		verStr := name[len("android-"):]
		ver, err := strconv.Atoi(verStr)
		if err != nil {
			continue
		}
		if ver < bestVer {
			continue
		}
		bestVer = ver
		bestPlat = platform
	}
	if bestPlat == "" {
		return "", fmt.Errorf("no Android platform installed in %q: install one with `sdkmanager \"platforms;android-35\"` (or Android Studio's SDK Manager, SDK Platforms tab); `giode doctor` lists anything else missing", sdk)
	}
	return bestPlat, nil
}

func latestCompiler(tcRoot, a string, minsdk int) (string, error) {
	arch := allArchs[a]
	allComps, err := filepath.Glob(filepath.Join(tcRoot, "bin", arch.clangArch+"*-clang"))
	if err != nil {
		return "", err
	}
	var bestVer int
	var firstVer int
	var bestCompiler string
	var firstCompiler string
	for _, compiler := range allComps {
		var ver int
		pattern := filepath.Join(tcRoot, "bin", arch.clangArch) + "%d-clang"
		if n, err := fmt.Sscanf(compiler, pattern, &ver); n < 1 || err != nil {
			continue
		}
		if firstCompiler == "" || ver < firstVer {
			firstVer = ver
			firstCompiler = compiler
		}
		if ver < bestVer {
			continue
		}
		if ver > minsdk {
			continue
		}
		bestVer = ver
		bestCompiler = compiler
	}
	if bestCompiler == "" {
		bestCompiler = firstCompiler
	}
	if bestCompiler == "" {
		return "", fmt.Errorf("no NDK compiler found for architecture %s in %s", a, tcRoot)
	}
	return bestCompiler, nil
}

func latestTools(sdk string) (string, error) {
	allTools, err := filepath.Glob(filepath.Join(sdk, "build-tools", "*"))
	if err != nil {
		return "", err
	}
	tools, found := latestVersionPath(allTools)
	if !found {
		return "", fmt.Errorf("no build-tools found in %q", sdk)
	}
	return tools, nil
}

// latestVersionFile finds the path with the highest version
// among paths on the form
//
//	/some/path/major.minor.patch
func latestVersionPath(paths []string) (string, bool) {
	var bestVer [3]int
	var bestDir string
loop:
	for _, path := range paths {
		name := filepath.Base(path)
		s := strings.SplitN(name, ".", 3)
		var version [3]int
		for i, v := range s {
			v, err := strconv.Atoi(v)
			if err != nil {
				continue loop
			}
			if v < bestVer[i] {
				continue loop
			}
			if v > bestVer[i] {
				break
			}
			version[i] = v
		}
		bestVer = version
		bestDir = path
	}
	return bestDir, bestDir != ""
}

func newZipWriter(w io.Writer) *zipWriter {
	return &zipWriter{
		w: zip.NewWriter(w),
	}
}

func (z *zipWriter) Close() error {
	err := z.w.Close()
	if z.err == nil {
		z.err = err
	}
	return z.err
}

func (z *zipWriter) Create(name string) io.Writer {
	if z.err != nil {
		return io.Discard
	}
	w, err := z.w.Create(name)
	if err != nil {
		z.err = err
		return io.Discard
	}
	return &errWriter{w: w, err: &z.err}
}

func (z *zipWriter) Store(name, file string) {
	z.add(name, file, false)
}

func (z *zipWriter) Add(name, file string) {
	z.add(name, file, true)
}

func (z *zipWriter) add(name, file string, compressed bool) {
	if z.err != nil {
		return
	}
	f, err := os.Open(file)
	if err != nil {
		z.err = err
		return
	}
	defer f.Close()
	fh := &zip.FileHeader{
		Name: name,
	}
	if compressed {
		fh.Method = zip.Deflate
	}
	w, err := z.w.CreateHeader(fh)
	if err != nil {
		z.err = err
		return
	}
	if _, err := io.Copy(w, f); err != nil {
		z.err = err
		return
	}
}

func (w *errWriter) Write(p []byte) (n int, err error) {
	if err := *w.err; err != nil {
		return 0, err
	}
	n, err = w.w.Write(p)
	*w.err = err
	return
}

// apkManifest is the AndroidManifest.xml template for APKs and bundles.
var apkManifest = template.Must(template.New("manifest").Parse(
	`<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
	package="{{.AppID}}"
	android:versionCode="{{.Version.VersionCode}}"
	android:versionName="{{.Version}}">
	{{if .PackageQueries}}
 	<queries>
	    {{range .PackageQueries}}
        <package android:name="{{.}}" />
        {{end}}
    </queries>
	{{end}}
	<uses-sdk android:minSdkVersion="{{.MinSDK}}" android:targetSdkVersion="{{.TargetSDK}}" />
{{range .Permissions}}	<uses-permission android:name="{{.}}"/>
{{end}}{{range .Features}}	<uses-feature android:{{.}} android:required="false"/>
{{end}}{{.ManifestXML}}
	<application {{.IconSnip}} android:label="{{.AppName}}"{{.ApplicationAttrs}}>
		<activity android:name="org.gioui.GioActivity"
			android:label="{{.AppName}}"
			android:theme="@style/Theme.GioApp"
			android:configChanges="screenSize|screenLayout|smallestScreenSize|orientation|keyboardHidden"
			android:windowSoftInputMode="adjustResize"
			android:launchMode="singleInstance"
			android:exported="true">
			<intent-filter>
				<action android:name="android.intent.action.MAIN" />
				<category android:name="android.intent.category.LAUNCHER" />
			</intent-filter>
			{{range .Schemes}}
			<intent-filter>
				<action android:name="android.intent.action.VIEW"></action>
				<category android:name="android.intent.category.DEFAULT"></category>
				<category android:name="android.intent.category.BROWSABLE"></category>
				<data android:scheme="{{.}}"></data>
			</intent-filter>
			{{end}}
{{.ActivityXML}}
		</activity>
{{.ApplicationXML}}
	</application>
</manifest>`))

// androidManifest renders the AndroidManifest.xml of an APK or bundle.
func androidManifest(d manifestData) ([]byte, error) {
	var b bytes.Buffer
	if err := apkManifest.Execute(&b, d); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// symbolsDir, when set, receives each ABI's unstripped library
// (Options.SymbolsDir).
var symbolsDir string

// splitSymbols copies the unstripped library to sym and strips lib with
// the NDK's llvm-strip.
func splitSymbols(tcRoot, lib, sym string) error {
	if err := os.MkdirAll(filepath.Dir(sym), 0o755); err != nil {
		return err
	}
	if err := copyFile(sym, lib); err != nil {
		return err
	}
	stripTool := filepath.Join(tcRoot, "bin", "llvm-strip")
	if runtime.GOOS == "windows" {
		stripTool += ".exe"
	}
	_, err := runCmd(exec.Command(stripTool, "--strip-all", lib))
	return err
}
