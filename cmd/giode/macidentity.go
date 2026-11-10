package main

import (
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// developerIDIdentity is the keychain's Developer ID Application
// signing identity, when there's exactly one, as in CI after importing
// MACOS_CERT_BASE64.
func developerIDIdentity() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command("security", "find-identity", "-v", "-p", "codesigning").Output()
	if err != nil {
		return ""
	}
	return parseDeveloperID(string(out))
}

// parseDeveloperID picks the Developer ID Application identity out of
// security find-identity's listing, or "" for none or several.
func parseDeveloperID(listing string) string {
	var found string
	for _, line := range strings.Split(listing, "\n") {
		i := strings.Index(line, `"Developer ID Application: `)
		if i < 0 {
			continue
		}
		name, _, ok := strings.Cut(line[i+1:], `"`)
		if !ok {
			continue
		}
		if found != "" && found != name {
			return ""
		}
		found = name
	}
	return found
}

var teamRE = regexp.MustCompile(`\(([A-Z0-9]{10})\)\s*$`)

// identityTeam is the team ID at the end of a signing identity's name.
func identityTeam(identity string) string {
	if m := teamRE.FindStringSubmatch(identity); m != nil {
		return m[1]
	}
	return ""
}
