package config

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"sort"
	"strings"
)

// Link is one of app.links: an https URL pattern, the path ending in *
// for everything below it.
type Link struct {
	Host, Path string
}

// ParseLinks parses app.links.
func (c *Config) ParseLinks() ([]Link, error) {
	var out []Link
	for _, s := range c.App.Links {
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return nil, fmt.Errorf("app.links: %q must be an https URL like https://example.com/app/*", s)
		}
		p := u.Path
		if p == "" {
			p = "/*"
		}
		out = append(out, Link{Host: u.Host, Path: p})
	}
	return out, nil
}

// LinkHosts returns the hosts of app.links, sorted.
func (c *Config) LinkHosts() []string {
	links, _ := c.ParseLinks()
	seen := map[string]bool{}
	var hosts []string
	for _, l := range links {
		if !seen[l.Host] {
			seen[l.Host] = true
			hosts = append(hosts, l.Host)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// PasskeyHosts returns app.passkeys' domains, sorted.
func (c *Config) PasskeyHosts() []string {
	seen := map[string]bool{}
	var hosts []string
	for _, h := range c.App.Passkeys {
		h = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "webcredentials:"), "/")
		if h != "" && !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// WellKnownHosts returns the hosts giode links writes files for: those
// of app.links and app.passkeys.
func (c *Config) WellKnownHosts() []string {
	seen := map[string]bool{}
	var hosts []string
	for _, h := range append(c.LinkHosts(), c.PasskeyHosts()...) {
		if !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// AndroidActivityXML returns the App Links intent filters for
// app.links, verified against each host's assetlinks.json.
func (c *Config) AndroidActivityXML() string {
	links, _ := c.ParseLinks()
	var b strings.Builder
	for _, l := range links {
		path := ""
		switch {
		case l.Path == "/*" || l.Path == "/":
		case strings.HasSuffix(l.Path, "/*") && !strings.Contains(strings.TrimSuffix(l.Path, "*"), "*"):
			path = ` android:pathPrefix="` + html.EscapeString(strings.TrimSuffix(l.Path, "*")) + `"`
		case strings.Contains(l.Path, "*"):
			path = ` android:pathPattern="` + html.EscapeString(strings.ReplaceAll(l.Path, "*", ".*")) + `"`
		default:
			path = ` android:path="` + html.EscapeString(l.Path) + `"`
		}
		fmt.Fprintf(&b, "\t\t\t<intent-filter android:autoVerify=\"true\">\n\t\t\t\t<action android:name=\"android.intent.action.VIEW\"/>\n"+
			"\t\t\t\t<category android:name=\"android.intent.category.DEFAULT\"/>\n\t\t\t\t<category android:name=\"android.intent.category.BROWSABLE\"/>\n"+
			"\t\t\t\t<data android:scheme=\"https\" android:host=\"%s\"%s/>\n\t\t\t</intent-filter>\n", html.EscapeString(l.Host), path)
	}
	return b.String()
}

// IOSEntitlements returns the entitlements giode adds to iOS signing:
// associated domains for app.links, aps-environment for ios.push.
func (c *Config) IOSEntitlements() map[string]any {
	ents := c.AppleEntitlements()
	if c.IOS.Push != "" {
		ents["aps-environment"] = c.IOS.Push
	}
	if c.App.ShareTarget {
		ents["com.apple.security.application-groups"] = []string{c.IOSAppGroup()}
	}
	return ents
}

// MacOSEntitlements returns the entitlements giode adds to macOS
// signing with a provisioning profile.
func (c *Config) MacOSEntitlements() map[string]any {
	ents := c.AppleEntitlements()
	if c.MacOS.Push != "" {
		ents["com.apple.developer.aps-environment"] = c.MacOS.Push
	}
	return ents
}

// AppleEntitlements returns the entitlements giode adds to iOS and
// macOS signing alike: associated domains for app.links and
// app.passkeys.
func (c *Config) AppleEntitlements() map[string]any {
	ents := map[string]any{}
	var domains []string
	for _, h := range c.LinkHosts() {
		domains = append(domains, "applinks:"+h)
	}
	for _, h := range c.PasskeyHosts() {
		domains = append(domains, "webcredentials:"+h)
	}
	if len(domains) > 0 {
		ents["com.apple.developer.associated-domains"] = domains
	}
	return ents
}

// AppleAppSiteAssociation returns the apple-app-site-association file
// for host: the paths of app.links on it, for the app with teamID.
func (c *Config) AppleAppSiteAssociation(host, teamID string) ([]byte, error) {
	links, err := c.ParseLinks()
	if err != nil {
		return nil, err
	}
	appID := teamID + "." + c.App.ID
	var comps []map[string]string
	for _, l := range links {
		if l.Host == host {
			comps = append(comps, map[string]string{"/": l.Path})
		}
	}
	file := map[string]any{}
	if len(comps) > 0 {
		file["applinks"] = map[string]any{
			"details": []any{map[string]any{"appIDs": []string{appID}, "components": comps}},
		}
	}
	if contains(c.PasskeyHosts(), host) {
		file["webcredentials"] = map[string]any{"apps": []string{appID}}
	}
	return json.MarshalIndent(file, "", "  ")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// AssetLinks returns host's assetlinks.json, letting the Android app
// with the given SHA-256 certificate fingerprints open app.links and use
// passkeys for app.passkeys.
func (c *Config) AssetLinks(host string, fingerprints []string) ([]byte, error) {
	var rel []string
	if contains(c.LinkHosts(), host) {
		rel = append(rel, "delegate_permission/common.handle_all_urls")
	}
	if contains(c.PasskeyHosts(), host) {
		rel = append(rel, "delegate_permission/common.get_login_creds")
	}
	return json.MarshalIndent([]any{map[string]any{
		"relation": rel,
		"target": map[string]any{
			"namespace":                "android_app",
			"package_name":             c.App.ID,
			"sha256_cert_fingerprints": fingerprints,
		},
	}}, "", "  ")
}
