package installer

import (
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScript(t *testing.T) {
	o := Options{Name: `My "App"`, ID: "com.example.app", Version: "1.2.3", Publisher: "Acme $ Co",
		Exe: `C:\b\MyApp.exe`, Output: `C:\b\setup.exe`, DesktopShortcut: true}
	s, err := Script(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`!define APPNAME "My $\"App$\""`,
		`RequestExecutionLevel user`,
		`InstallDir "$LOCALAPPDATA\Programs\${APPNAME}"`,
		`"Publisher" "Acme $$ Co"`,
		`CreateShortCut "$DESKTOP\${APPNAME}.lnk"`,
		`VIProductVersion "1.2.3.0"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %s", want)
		}
	}
	if strings.Contains(s, "MUI_PAGE_LICENSE") {
		t.Error("license page without a license")
	}
	o.PerMachine, o.License = true, "license.txt"
	s, _ = Script(o)
	for _, want := range []string{"RequestExecutionLevel admin", `$PROGRAMFILES64`, "!define REGROOT HKLM", "MUI_PAGE_LICENSE"} {
		if !strings.Contains(s, want) {
			t.Errorf("per-machine script lacks %s", want)
		}
	}
}

func TestWriteICO(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "icon.png")
	f, _ := os.Create(src)
	png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 512, 512)))
	f.Close()
	dst := filepath.Join(dir, "app.ico")
	if err := writeICO(src, dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	if len(b) < 6 || binary.LittleEndian.Uint16(b[2:]) != 1 || binary.LittleEndian.Uint16(b[4:]) != 7 {
		t.Fatalf("bad ICO header % x", b[:6])
	}
	// The last entry's offset and size reach the end of the file.
	e := b[6+16*6:]
	size, off := binary.LittleEndian.Uint32(e[8:]), binary.LittleEndian.Uint32(e[12:])
	if int(size+off) != len(b) || e[0] != 0 {
		t.Errorf("256px entry: size %d offset %d width byte %d, file %d", size, off, e[0], len(b))
	}
}

func TestBuild(t *testing.T) {
	if _, err := findMakensis(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "App.exe")
	os.WriteFile(exe, []byte("MZ"), 0o755)
	out := filepath.Join(dir, "out", "App-setup.exe")
	if err := Build(Options{Name: "App", ID: "com.example.app", Version: "1.0.0", Publisher: "Acme", Exe: exe, Output: out}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil || len(b) < 2 || string(b[:2]) != "MZ" {
		t.Errorf("setup program not written: %v", err)
	}
}

func TestScript_ContextMenu(t *testing.T) {
	o := Options{Name: "App", ID: "com.example.app", Version: "1.0.0", Exe: `C:\b\App.exe`, Output: `C:\b\setup.exe`,
		ContextMenu: []Verb{{Key: `*\shell\com-example-app.ask`, Label: "Ask App", ID: "ask", Arg: "%1"}}}
	s, err := Script(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`WriteRegStr ${REGROOT} "Software\Classes\*\shell\com-example-app.ask" "MUIVerb" "Ask App"`,
		`WriteRegStr ${REGROOT} "Software\Classes\*\shell\com-example-app.ask\command" "" '"$INSTDIR\${EXE}" --giode-action=ask "%1"'`,
		`DeleteRegKey ${REGROOT} "Software\Classes\*\shell\com-example-app.ask"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %s", want)
		}
	}
}

// TestBuild_ContextMenuUnderWine installs and uninstalls a setup
// program with a context menu entry under Wine, checking the registry.
func TestBuild_ContextMenuUnderWine(t *testing.T) {
	if _, err := findMakensis(); err != nil {
		t.Skip(err)
	}
	wine, err := exec.LookPath("wine")
	if err != nil {
		t.Skip("no wine")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "CtxApp.exe")
	os.WriteFile(exe, []byte("MZ"), 0o755)
	out := filepath.Join(dir, "setup.exe")
	key := `HKCU\Software\Classes\*\shell\com-example-ctxapp.ask`
	o := Options{Name: "CtxApp", ID: "com.example.ctxapp", Version: "1.0.0", Publisher: "Acme", Exe: exe, Output: out,
		ContextMenu: []Verb{{Key: `*\shell\com-example-ctxapp.ask`, Label: "Ask CtxApp", ID: "ask", Arg: "%1"}}}
	if err := Build(o); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		cmd := exec.Command(wine, args...)
		cmd.Env = append(os.Environ(), "WINEDEBUG=-all")
		b, _ := cmd.CombinedOutput()
		return string(b)
	}
	run(out, "/S")
	defer run("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\com.example.ctxapp`, "/f")
	if got := run("reg", "query", key+`\command`); !strings.Contains(got, `--giode-action=ask "%1"`) {
		t.Fatalf("command after install:\n%s", got)
	}
	if got := run("reg", "query", key, "/v", "MUIVerb"); !strings.Contains(got, "Ask CtxApp") {
		t.Errorf("label after install:\n%s", got)
	}
	// Uninstall: the uninstaller re-runs itself from a temp copy, so
	// wait for the key to go.
	uninst := strings.TrimSpace(run("cmd", "/c", `echo %LOCALAPPDATA%`)) + `\Programs\CtxApp\Uninstall.exe`
	run(uninst, "/S")
	for i := 0; i < 50; i++ {
		if got := run("reg", "query", key); !strings.Contains(got, "MUIVerb") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Error("context menu key left after uninstalling")
}

func TestScript_FilesAndFileTypes(t *testing.T) {
	s, err := Script(Options{Name: "Player", ID: "com.example.player", Version: "1.0.0", Exe: `C:\b\Player.exe`, Output: "setup.exe",
		Files:     []string{"/b/libmpv-2.dll", "/b/avcodec-61.dll"},
		FileTypes: []FileType{{ProgID: "com.example.player.mkv", Name: "MKV video", Extensions: []string{".mkv", ".mk3d"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		`File "/b/libmpv-2.dll"`,
		`Delete "$INSTDIR\libmpv-2.dll"`,
		`WriteRegStr ${REGROOT} "Software\Classes\com.example.player.mkv" "" "MKV video"`,
		`WriteRegStr ${REGROOT} "Software\Classes\com.example.player.mkv\shell\open\command" "" '"$INSTDIR\${EXE}" "%1"'`,
		`WriteRegStr ${REGROOT} "Software\Classes\.mkv\OpenWithProgids" "com.example.player.mkv" ""`,
		`DeleteRegValue ${REGROOT} "Software\Classes\.mk3d\OpenWithProgids" "com.example.player.mkv"`,
		`SHChangeNotify`,
	} {
		if !strings.Contains(s, w) {
			t.Errorf("script lacks %s", w)
		}
	}
}

// TestBuild_FileTypesUnderWine installs and uninstalls a setup program
// registering a file type and a bundled DLL.
func TestBuild_FileTypesUnderWine(t *testing.T) {
	if _, err := findMakensis(); err != nil {
		t.Skip(err)
	}
	wine, err := exec.LookPath("wine")
	if err != nil {
		t.Skip("no wine")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "FtApp.exe")
	os.WriteFile(exe, []byte("MZ"), 0o755)
	dll := filepath.Join(dir, "libmpv-2.dll")
	os.WriteFile(dll, []byte("MZ dll"), 0o644)
	out := filepath.Join(dir, "setup.exe")
	o := Options{Name: "FtApp", ID: "com.example.ftapp", Version: "1.0.0", Publisher: "Acme", Exe: exe, Output: out,
		Files:     []string{dll},
		FileTypes: []FileType{{ProgID: "com.example.ftapp.ftv", Name: "FT video", Extensions: []string{".ftv"}}}}
	if err := Build(o); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		cmd := exec.Command(wine, args...)
		cmd.Env = append(os.Environ(), "WINEDEBUG=-all")
		b, _ := cmd.CombinedOutput()
		return string(b)
	}
	run(out, "/S")
	defer run("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\com.example.ftapp`, "/f")
	if got := run("reg", "query", `HKCU\Software\Classes\com.example.ftapp.ftv\shell\open\command`); !strings.Contains(got, `FtApp.exe" "%1"`) {
		t.Fatalf("open command after install:\n%s", got)
	}
	if got := run("reg", "query", `HKCU\Software\Classes\.ftv\OpenWithProgids`); !strings.Contains(got, "com.example.ftapp.ftv") {
		t.Errorf("OpenWithProgids:\n%s", got)
	}
	appDir := strings.TrimSpace(run("cmd", "/c", `echo %LOCALAPPDATA%`)) + `\Programs\FtApp`
	if got := run("cmd", "/c", "dir", "/b", appDir); !strings.Contains(got, "libmpv-2.dll") {
		t.Errorf("installed files:\n%s", got)
	}
	run(appDir+`\Uninstall.exe`, "/S")
	for i := 0; i < 50; i++ {
		if got := run("reg", "query", `HKCU\Software\Classes\com.example.ftapp.ftv`); !strings.Contains(got, "FT video") {
			if got := run("reg", "query", `HKCU\Software\Classes\.ftv\OpenWithProgids`); strings.Contains(got, "com.example.ftapp.ftv") {
				t.Error("OpenWithProgids entry left after uninstalling")
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Error("file type left after uninstalling")
}

func TestScript_Firewall(t *testing.T) {
	for _, perMachine := range []bool{false, true} {
		o := Options{Name: "Easy Send", ID: "app.easysend", Version: "1.0.0", Exe: `C:\b\EasySend.exe`, Output: `C:\b\setup.exe`, PerMachine: perMachine, Firewall: true}
		s, err := Script(o)
		if err != nil {
			t.Fatal(err)
		}
		wants := []string{
			`!define FW_ADD 'advfirewall firewall add rule name="${APPNAME}" dir=in action=allow enable=yes profile=private,domain program="$INSTDIR\${EXE}"'`,
			`!define FW_DELETE 'advfirewall firewall delete rule name="${APPNAME}" dir=in'`,
		}
		if perMachine {
			wants = append(wants, `nsExec::Exec 'netsh ${FW_ADD}'`)
		} else {
			wants = append(wants, `ExecShellWait "runas" "netsh" '${FW_ADD}' SW_HIDE`)
		}
		for _, w := range wants {
			if !strings.Contains(s, w) {
				t.Errorf("per machine %v: script lacks %s", perMachine, w)
			}
		}
		// Removed on uninstall.
		if i := strings.Index(s, `Section "Uninstall"`); i < 0 || !strings.Contains(s[i:], "FW_DELETE") {
			t.Errorf("per machine %v: the uninstaller doesn't remove the rule", perMachine)
		}
	}
	s, _ := Script(Options{Name: "A", ID: "a.b", Version: "1.0.0", Exe: `C:\b\A.exe`, Output: `C:\b\s.exe`, Firewall: true, FirewallPublic: true})
	if !strings.Contains(s, "profile=any") {
		t.Error("FirewallPublic didn't allow every profile")
	}
	if s, _ := Script(Options{Name: "A", ID: "a.b", Version: "1.0.0", Exe: `C:\b\A.exe`, Output: `C:\b\s.exe`}); strings.Contains(s, "netsh") {
		t.Error("a firewall rule without Firewall")
	}
}

func TestBuild_Firewall(t *testing.T) {
	if _, err := findMakensis(); err != nil {
		t.Skip(err)
	}
	for _, perMachine := range []bool{false, true} {
		dir := t.TempDir()
		exe := filepath.Join(dir, "App.exe")
		os.WriteFile(exe, []byte("MZ"), 0o755)
		out := filepath.Join(dir, "App-setup.exe")
		if err := Build(Options{Name: "App", ID: "com.example.app", Version: "1.0.0", Exe: exe, Output: out, PerMachine: perMachine, Firewall: true}); err != nil {
			t.Fatalf("per machine %v: %v", perMachine, err)
		}
	}
}
