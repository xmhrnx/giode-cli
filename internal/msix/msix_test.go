package msix

import (
	"archive/zip"
	"encoding/xml"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xmhrnx/giode-cli/internal/sign"
)

func testApp(t *testing.T) App {
	dir := t.TempDir()
	exe := filepath.Join(dir, "My App.exe")
	os.WriteFile(exe, append([]byte("MZ"), make([]byte, 200000)...), 0o644)
	icon := filepath.Join(dir, "icon.png")
	f, _ := os.Create(icon)
	png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 256, 256)))
	f.Close()
	return App{Name: "My App", IdentityName: "com.example.myapp", Publisher: "CN=Example Ltd", Version: "1.2.3",
		Exe: exe, Icon: icon, Arch: "amd64", Protocols: []string{"myapp"}}
}

func TestWrite(t *testing.T) {
	a := testApp(t)
	out := filepath.Join(t.TempDir(), "app.msix")
	if err := Write(a, out); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var names []string
	files := map[string]string{}
	for _, f := range r.File {
		names = append(names, f.Name)
		if f.Method != zip.Store || f.Flags&0x8 != 0 {
			t.Errorf("%s: method %d flags %x; want stored without a data descriptor", f.Name, f.Method, f.Flags)
		}
		if strings.HasSuffix(f.Name, ".xml") {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			files[f.Name] = string(b)
		}
	}
	// The first entry's data starts right after its local header: the
	// block map's LfhSize.
	if off, _ := r.File[0].DataOffset(); off != 42 {
		t.Errorf("first local header is %d bytes, block map says 42", off)
	}
	if names[0] != "My%20App.exe" || names[len(names)-1] != "[Content_Types].xml" || names[len(names)-2] != "AppxBlockMap.xml" {
		t.Errorf("entries %v", names)
	}
	m := files["AppxManifest.xml"]
	for _, w := range []string{`<Identity Name="com.example.myapp" Publisher="CN=Example Ltd" Version="1.2.3.0" ProcessorArchitecture="x64"/>`, `Executable="My App.exe"`, `<uap:Protocol Name="myapp"/>`, `runFullTrust`} {
		if !strings.Contains(m, w) {
			t.Errorf("manifest lacks %s", w)
		}
	}
	bm := files["AppxBlockMap.xml"]
	// 200002 bytes: four 64KB blocks.
	if !strings.Contains(bm, `<File Name="My App.exe" Size="200002" LfhSize="42">`) || strings.Count(bm[:strings.Index(bm, "</File>")], "<Block ") != 4 {
		t.Errorf("block map:\n%s", bm[:min(len(bm), 600)])
	}
	if !strings.Contains(bm, `Name="Assets\Square44x44Logo.png"`) || strings.Contains(bm, "Content_Types") {
		t.Error("block map files wrong")
	}
}

// TestSign signs the package with osslsigncode, which parses and checks
// the package structure, and verifies the signature.
func TestSign(t *testing.T) {
	tool, err := exec.LookPath("osslsigncode")
	if err != nil {
		t.Skip("no osslsigncode")
	}
	a := testApp(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "app.msix")
	if err := Write(a, out); err != nil {
		t.Fatal(err)
	}
	pfx := filepath.Join(dir, "cert.pfx")
	if err := sign.SelfSigned(pfx, "Example Ltd", "pw", 1); err != nil {
		t.Fatal(err)
	}
	signed := filepath.Join(dir, "signed.msix")
	if b, err := exec.Command(tool, "sign", "-pkcs12", pfx, "-pass", "pw", "-in", out, "-out", signed).CombinedOutput(); err != nil {
		t.Fatalf("osslsigncode sign: %v\n%s", err, b)
	}
	b, _ := exec.Command(tool, "verify", "-in", signed).CombinedOutput()
	if !strings.Contains(string(b), "Signature verification: ok") && !strings.Contains(string(b), "Message digest") {
		t.Errorf("osslsigncode verify:\n%s", b)
	}
	if !strings.Contains(string(b), "Calculated") || strings.Contains(string(b), "MISMATCH") {
		t.Errorf("digest check:\n%s", b)
	}
	r, err := zip.OpenReader(signed)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range r.File {
		found = found || f.Name == "AppxSignature.p7x"
	}
	r.Close()
	if !found {
		t.Error("no AppxSignature.p7x after signing")
	}
}

func TestManifest_StartupTask(t *testing.T) {
	a := App{Name: "Agent", IdentityName: "com.example.agent", Publisher: "CN=Example", Version: "1.0.0"}
	m, err := Manifest(a, "x64", "Agent.exe")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m, "windows.startupTask") {
		t.Error("startup task without StartupTask")
	}
	a.StartupTask = true
	if m, err = Manifest(a, "x64", "Agent.exe"); err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal([]byte(m), new(struct{})); err != nil {
		t.Fatalf("manifest isn't XML: %v\n%s", err, m)
	}
	for _, w := range []string{`IgnorableNamespaces="uap rescap desktop"`, `<desktop:Extension Category="windows.startupTask" Executable="Agent.exe" EntryPoint="Windows.FullTrustApplication">`,
		`<desktop:StartupTask TaskId="GiodeLaunchAtLogin" Enabled="false" DisplayName="Agent"/>`} {
		if !strings.Contains(m, w) {
			t.Errorf("lacks %s:\n%s", w, m)
		}
	}
}

func TestManifest_FileTypes(t *testing.T) {
	m, err := Manifest(App{Name: "Player", IdentityName: "com.example.player", Publisher: "CN=Example", Version: "1.0.0",
		FileTypes: []FileType{{Name: "mkv", DisplayName: "MKV video", Extensions: []string{".mkv", ".mk3d"}}}}, "x64", "Player.exe")
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal([]byte(m), new(struct{})); err != nil {
		t.Fatalf("manifest isn't XML: %v\n%s", err, m)
	}
	for _, w := range []string{`Category="windows.fileTypeAssociation"`, `<uap:FileTypeAssociation Name="mkv">`, `<uap:FileType>.mk3d</uap:FileType>`} {
		if !strings.Contains(m, w) {
			t.Errorf("lacks %s:\n%s", w, m)
		}
	}
}
