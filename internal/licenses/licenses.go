// Package licenses collects the licenses of the Go modules an app is
// built from, for the open-source notices apps must show.
package licenses

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// License is a module's license.
type License struct {
	Module  string `json:"module"`
	Version string `json:"version,omitempty"`
	// Type is the SPDX identifier when recognized ("MIT"), else "".
	Type string `json:"type,omitempty"`
	// Text is the license file's text (and any NOTICE file's).
	Text string `json:"text"`
}

// platforms are the GOOS values whose dependencies are collected: an
// app's modules differ by platform.
var platforms = []string{"linux", "windows", "darwin", "android", "ios", "js"}

// Collect returns the licenses of the modules package pkg (in dir)
// depends on, on any platform, and of Go's standard library. Modules
// without a license file are listed with an empty text.
func Collect(dir, pkg, tags string) ([]License, error) {
	mods := map[string]modInfo{}
	for _, goos := range platforms {
		args := []string{"list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{if .Replace}}\t{{.Replace.Dir}}{{end}}{{end}}{{end}}"}
		if tags != "" {
			args = append(args, "-tags", tags)
		}
		args = append(args, pkg)
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=1", "GOFLAGS=-mod=mod")
		if goos == "js" {
			cmd.Env = append(cmd.Env, "GOARCH=wasm")
		}
		out, err := cmd.Output()
		if err != nil {
			// A package that doesn't build for this platform.
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			f := strings.Split(sc.Text(), "\t")
			if len(f) < 3 || f[0] == "" {
				continue
			}
			m := modInfo{path: f[0], version: f[1], dir: f[2]}
			if len(f) > 3 && f[3] != "" {
				m.dir = f[3]
			}
			mods[m.path] = m
		}
	}
	if len(mods) == 0 {
		return nil, fmt.Errorf("go list found no dependencies of %s", pkg)
	}
	var out []License
	if goroot, err := exec.Command("go", "env", "GOROOT").Output(); err == nil {
		if text := readLicense(strings.TrimSpace(string(goroot))); text != "" {
			out = append(out, License{Module: "Go standard library", Type: Detect(text), Text: text})
		}
	}
	paths := make([]string, 0, len(mods))
	for p := range mods {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		m := mods[p]
		text := readLicense(m.dir)
		out = append(out, License{Module: m.path, Version: m.version, Type: Detect(text), Text: text})
	}
	return out, nil
}

type modInfo struct{ path, version, dir string }

// licenseFile matches license files: LICENSE, COPYING, LICENSE-MIT, and
// those named after what they cover, such as a font's SIL-LICENSE or
// OFL.txt.
var licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|unlicense)(\.(md|txt|rst))?$|^licen[cs]e[-_.]|[-_]licen[cs]e(\.(md|txt))?$|^ofl(\.(md|txt))?$`)

// isLicenseFile reports whether name is a license file, not code that
// happens to be named after one (license.go).
func isLicenseFile(name string) bool {
	return licenseFile.MatchString(name) && !strings.HasSuffix(strings.ToLower(name), ".go")
}

var noticeFile = regexp.MustCompile(`(?i)^notice(\.(md|txt))?$`)

// readLicense returns a module directory's license text, with its
// NOTICE file appended.
func readLicense(dir string) string {
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var lic, notice []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch {
		case isLicenseFile(e.Name()):
			lic = append(lic, e.Name())
		case noticeFile.MatchString(e.Name()):
			notice = append(notice, e.Name())
		}
	}
	sort.Strings(lic)
	var parts []string
	for _, n := range append(lic, notice...) {
		if b, err := os.ReadFile(filepath.Join(dir, n)); err == nil {
			parts = append(parts, strings.TrimSpace(string(b)))
		}
	}
	return strings.Join(parts, "\n\n")
}

// Detect recognizes common licenses from their text. A module whose
// code and font are licensed apart gets both: "BSD-3-Clause AND OFL-1.1".
func Detect(text string) string {
	t := strings.ToLower(strings.Join(strings.Fields(text), " "))
	main := detect(t)
	if strings.Contains(t, "sil open font license") {
		if main == "" {
			return "OFL-1.1"
		}
		return main + " AND OFL-1.1"
	}
	return main
}

func detect(t string) string {
	switch {
	case t == "":
		return ""
	case strings.Contains(t, "apache license") && strings.Contains(t, "version 2.0"):
		return "Apache-2.0"
	case strings.Contains(t, "mozilla public license") && strings.Contains(t, "2.0"):
		return "MPL-2.0"
	case strings.Contains(t, "this is free and unencumbered software released into the public domain"):
		if strings.Contains(t, "permission is hereby granted") {
			return "Unlicense OR MIT"
		}
		return "Unlicense"
	case strings.Contains(t, "permission is hereby granted, free of charge"):
		return "MIT"
	case strings.Contains(t, "permission to use, copy, modify, and/or distribute this software for any purpose"):
		return "ISC"
	case strings.Contains(t, "redistribution and use in source and binary forms"):
		if strings.Contains(t, "neither the name") || strings.Contains(t, "names of its contributors") {
			return "BSD-3-Clause"
		}
		return "BSD-2-Clause"
	case strings.Contains(t, "this software is provided 'as-is'") && strings.Contains(t, "altered source versions"):
		return "Zlib"
	case strings.Contains(t, "gnu general public license"):
		return "GPL"
	case strings.Contains(t, "gnu lesser general public license"):
		return "LGPL"
	}
	return ""
}
