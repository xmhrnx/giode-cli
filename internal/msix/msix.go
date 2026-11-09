// Package msix writes MSIX packages: the format of the Microsoft Store
// and of App Installer, which installs and updates apps cleanly
// without a setup program. A package is a ZIP of the app's files with
// a manifest, a block map of their hashes and a content types list;
// signing (osslsigncode or signtool) adds the signature.
package msix

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"hash/crc32"
	"html"
	"image"
	"image/png"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	_ "image/jpeg"

	"golang.org/x/image/draw"
)

// App describes the app to package.
type App struct {
	// Name is the display name; IdentityName the package name (the
	// Store reserves it; the app ID by default).
	Name, IdentityName string
	// Publisher is the signing certificate's subject ("CN=Example
	// Ltd, O=Example Ltd, C=US"), which must match it exactly;
	// PublisherDisplayName what's shown.
	Publisher, PublisherDisplayName string
	// Description is shown by Windows.
	Description string
	// Version is major.minor.patch.
	Version string
	// Exe is the built program; Icon a square PNG or JPEG.
	Exe, Icon string
	// Arch is the GOARCH: amd64, arm64 or 386.
	Arch string
	// Protocols are URL schemes the app handles.
	Protocols []string
	// Files are packaged next to the program: the DLLs it loads.
	Files []string
	// FileTypes are file types the app opens.
	FileTypes []FileType
	// StartupTask declares a startup task, off until the app turns it
	// on with giode's App.SetLaunchAtLogin: a packaged app can't use
	// the Run key.
	StartupTask bool
	// Firewall declares inbound Windows Firewall rules for the program:
	// a package can't add them at install the way a setup program does.
	// With Firewall set and no FirewallPorts, the program is allowed on
	// every TCP and UDP port. FirewallPublic allows public networks too,
	// not only private and domain ones.
	Firewall       bool
	FirewallPublic bool
	FirewallPorts  []FirewallPort
}

// FirewallPort is an inbound rule: a protocol, "TCP" or "UDP", and a
// local port range.
type FirewallPort struct {
	Protocol         string
	PortMin, PortMax int
}

// StartupTaskID is the startup task's TaskId; giode looks it up by it.
const StartupTaskID = "GiodeLaunchAtLogin"

// FileType is a file type association: a name (lower case letters,
// digits, -, _ and .), a description and extensions with the dot.
type FileType struct {
	Name, DisplayName string
	Extensions        []string
}

var archNames = map[string]string{"amd64": "x64", "arm64": "arm64", "386": "x86"}

// file is a package entry.
type file struct {
	name string
	body []byte
}

// Write writes the package to out.
func Write(a App, out string) error {
	arch, ok := archNames[a.Arch]
	if !ok {
		return fmt.Errorf("no MSIX architecture for GOARCH %s", a.Arch)
	}
	exe, err := os.ReadFile(a.Exe)
	if err != nil {
		return err
	}
	exeName := path.Base(strings.ReplaceAll(a.Exe, `\`, "/"))
	files := []file{{name: exeName, body: exe}}
	for _, f := range a.Files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		files = append(files, file{name: filepath.Base(f), body: b})
	}
	logos := map[string]int{"Square44x44Logo.png": 44, "Square150x150Logo.png": 150, "Wide310x150Logo.png": 0, "StoreLogo.png": 50}
	if a.Icon == "" {
		return fmt.Errorf("an MSIX package needs an icon: set app.icon in giode.toml")
	}
	names := make([]string, 0, len(logos))
	for n := range logos {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := logo(a.Icon, logos[n])
		if err != nil {
			return fmt.Errorf("icon: %w", err)
		}
		files = append(files, file{name: "Assets/" + n, body: b})
	}
	manifest, err := Manifest(a, arch, exeName)
	if err != nil {
		return err
	}
	files = append(files, file{name: "AppxManifest.xml", body: []byte(manifest)})

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var blockMap strings.Builder
	blockMap.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="no"?>` + "\r\n")
	blockMap.WriteString(`<BlockMap xmlns="http://schemas.microsoft.com/appx/2010/blockmap" HashMethod="http://www.w3.org/2001/04/xmlenc#sha256">`)
	for _, f := range files {
		zipName := encodeName(f.name)
		if err := store(zw, zipName, f.body); err != nil {
			return err
		}
		winName := strings.ReplaceAll(f.name, "/", `\`)
		fmt.Fprintf(&blockMap, `<File Name="%s" Size="%d" LfhSize="%d">`, html.EscapeString(winName), len(f.body), 30+len(zipName))
		for off := 0; off < len(f.body); off += 65536 {
			end := min(off+65536, len(f.body))
			sum := sha256.Sum256(f.body[off:end])
			fmt.Fprintf(&blockMap, `<Block Hash="%s"/>`, base64.StdEncoding.EncodeToString(sum[:]))
		}
		blockMap.WriteString(`</File>`)
	}
	blockMap.WriteString(`</BlockMap>`)
	if err := store(zw, "AppxBlockMap.xml", []byte(blockMap.String())); err != nil {
		return err
	}
	if err := store(zw, "[Content_Types].xml", []byte(contentTypes(files))); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(out, buf.Bytes(), 0o644)
}

// store adds an uncompressed entry, with its sizes in the local header
// (no data descriptor), as packaging tools expect.
func store(zw *zip.Writer, name string, body []byte) error {
	h := &zip.FileHeader{Name: name, Method: zip.Store, CRC32: crc32.ChecksumIEEE(body),
		CompressedSize64: uint64(len(body)), UncompressedSize64: uint64(len(body))}
	w, err := zw.CreateRaw(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, bytes.NewReader(body))
	return err
}

// encodeName percent-encodes characters OPC part names can't hold.
func encodeName(n string) string {
	var b strings.Builder
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("-._~/!$&'()*+,;=:@", r):
			b.WriteRune(r)
		default:
			for _, c := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
	}
	return b.String()
}

// contentTypes lists the media types of the package's files.
func contentTypes(files []file) string {
	types := map[string]string{"exe": "application/x-msdownload", "png": "image/png", "dll": "application/x-msdownload", "xml": "application/vnd.ms-appx.manifest+xml"}
	seen := map[string]bool{}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\r\n")
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	for _, f := range files {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(f.name), "."))
		if ext == "xml" || seen[ext] {
			continue
		}
		seen[ext] = true
		t := types[ext]
		if t == "" {
			t = "application/octet-stream"
		}
		fmt.Fprintf(&b, `<Default Extension="%s" ContentType="%s"/>`, ext, t)
	}
	b.WriteString(`<Override PartName="/AppxManifest.xml" ContentType="application/vnd.ms-appx.manifest+xml"/>`)
	b.WriteString(`<Override PartName="/AppxBlockMap.xml" ContentType="application/vnd.ms-appx.blockmap+xml"/>`)
	b.WriteString(`<Override PartName="/AppxSignature.p7x" ContentType="application/vnd.ms-appx.signature"/>`)
	b.WriteString(`</Types>`)
	return b.String()
}

var identityRE = regexp.MustCompile(`[^A-Za-z0-9.-]`)

// Manifest returns the AppxManifest.xml of a full-trust desktop app.
func Manifest(a App, arch, exe string) (string, error) {
	parts := strings.Split(a.Version, ".")
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	version := strings.Join(parts[:3], ".") + ".0"
	name := identityRE.ReplaceAllString(a.IdentityName, "-")
	if len(name) < 3 {
		return "", fmt.Errorf("the MSIX identity name %q is too short", a.IdentityName)
	}
	publisher := a.Publisher
	if publisher == "" {
		return "", fmt.Errorf("an MSIX package needs windows.msix.publisher: the signing certificate's subject, like \"CN=Example Ltd\"")
	}
	display := a.PublisherDisplayName
	if display == "" {
		display = strings.TrimPrefix(strings.SplitN(publisher, ",", 2)[0], "CN=")
	}
	desc := a.Description
	if desc == "" {
		desc = a.Name
	}
	e := html.EscapeString
	var ext strings.Builder
	if len(a.Protocols) > 0 || len(a.FileTypes) > 0 || a.StartupTask {
		ext.WriteString("\n      <Extensions>")
		for _, p := range a.Protocols {
			fmt.Fprintf(&ext, "\n        <uap:Extension Category=\"windows.protocol\"><uap:Protocol Name=\"%s\"/></uap:Extension>", e(strings.ToLower(p)))
		}
		for _, t := range a.FileTypes {
			fmt.Fprintf(&ext, "\n        <uap:Extension Category=\"windows.fileTypeAssociation\"><uap:FileTypeAssociation Name=\"%s\"><uap:DisplayName>%s</uap:DisplayName><uap:SupportedFileTypes>", e(t.Name), e(t.DisplayName))
			for _, x := range t.Extensions {
				fmt.Fprintf(&ext, "<uap:FileType>%s</uap:FileType>", e(x))
			}
			ext.WriteString("</uap:SupportedFileTypes></uap:FileTypeAssociation></uap:Extension>")
		}
		if a.StartupTask {
			fmt.Fprintf(&ext, "\n        <desktop:Extension Category=\"windows.startupTask\" Executable=\"%s\" EntryPoint=\"Windows.FullTrustApplication\"><desktop:StartupTask TaskId=\"%s\" Enabled=\"false\" DisplayName=\"%s\"/></desktop:Extension>", e(exe), StartupTaskID, e(a.Name))
		}
		ext.WriteString("\n      </Extensions>")
	}
	pkgExt := firewallExtension(a, exe)
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10"
  xmlns:uap="http://schemas.microsoft.com/appx/manifest/uap/windows10"
  xmlns:rescap="http://schemas.microsoft.com/appx/manifest/foundation/windows10/restrictedcapabilities"
  xmlns:desktop="http://schemas.microsoft.com/appx/manifest/desktop/windows10"
  xmlns:desktop2="http://schemas.microsoft.com/appx/manifest/desktop/windows10/2"
  IgnorableNamespaces="uap rescap desktop desktop2">
  <Identity Name="%s" Publisher="%s" Version="%s" ProcessorArchitecture="%s"/>
  <Properties>
    <DisplayName>%s</DisplayName>
    <PublisherDisplayName>%s</PublisherDisplayName>
    <Logo>Assets\StoreLogo.png</Logo>
  </Properties>
  <Dependencies>
    <TargetDeviceFamily Name="Windows.Desktop" MinVersion="10.0.17763.0" MaxVersionTested="10.0.26100.0"/>
  </Dependencies>
  <Resources>
    <Resource Language="en-us"/>
  </Resources>
  <Applications>
    <Application Id="App" Executable="%s" EntryPoint="Windows.FullTrustApplication">
      <uap:VisualElements DisplayName="%s" Description="%s" BackgroundColor="transparent"
        Square150x150Logo="Assets\Square150x150Logo.png" Square44x44Logo="Assets\Square44x44Logo.png">
        <uap:DefaultTile Wide310x150Logo="Assets\Wide310x150Logo.png"/>
      </uap:VisualElements>%s
    </Application>
  </Applications>
  <Capabilities>
    <rescap:Capability Name="runFullTrust"/>
  </Capabilities>%s
</Package>
`, e(name), e(publisher), version, arch, e(a.Name), e(display), e(exe), e(a.Name), e(desc), ext.String(), pkgExt), nil
}

// firewallExtension is the package's windows.firewallRules extension,
// or "" without Firewall.
func firewallExtension(a App, exe string) string {
	if !a.Firewall && len(a.FirewallPorts) == 0 {
		return ""
	}
	profile := "domainAndPrivate"
	if a.FirewallPublic {
		profile = "all"
	}
	ports := a.FirewallPorts
	if len(ports) == 0 {
		ports = []FirewallPort{{Protocol: "TCP"}, {Protocol: "UDP"}}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  <Extensions>\n    <desktop2:Extension Category=\"windows.firewallRules\">\n      <desktop2:FirewallRules Executable=\"%s\">", html.EscapeString(exe))
	for _, p := range ports {
		fmt.Fprintf(&b, "\n        <desktop2:Rule Direction=\"in\" IPProtocol=\"%s\"", p.Protocol)
		if p.PortMin > 0 {
			fmt.Fprintf(&b, " LocalPortMin=\"%d\" LocalPortMax=\"%d\"", p.PortMin, max(p.PortMax, p.PortMin))
		}
		fmt.Fprintf(&b, " Profile=\"%s\"/>", profile)
	}
	b.WriteString("\n      </desktop2:FirewallRules>\n    </desktop2:Extension>\n  </Extensions>")
	return b.String()
}

// logo renders the icon as a square logo of size, or a 310x150 wide
// logo with the icon centered when size is 0.
func logo(src string, size int) ([]byte, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	var out *image.NRGBA
	if size == 0 {
		out = image.NewNRGBA(image.Rect(0, 0, 310, 150))
		draw.CatmullRom.Scale(out, image.Rect(95, 15, 215, 135), img, img.Bounds(), draw.Over, nil)
	} else {
		out = image.NewNRGBA(image.Rect(0, 0, size, size))
		draw.CatmullRom.Scale(out, out.Bounds(), img, img.Bounds(), draw.Over, nil)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, out); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
