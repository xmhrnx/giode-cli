// Package installer makes Windows setup programs with NSIS.
package installer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	_ "image/jpeg"

	"golang.org/x/image/draw"

	"github.com/xmhrnx/giode-cli/internal/progress"
)

// Options describe the app to install.
type Options struct {
	// Name is the display name; ID the app ID, which keys the
	// uninstall entry.
	Name, ID string
	// Version is major.minor.patch, Publisher and Website what Windows'
	// list of installed apps shows.
	Version, Publisher, Website string
	// Exe is the built program; Icon a PNG for the setup program's icon
	// (optional); License a text file to accept (optional).
	Exe, Icon, License string
	// Files are installed next to the program: the DLLs it loads.
	Files []string
	// FileTypes are registered for Explorer's "Open with", and removed
	// on uninstall.
	FileTypes []FileType
	// PerMachine installs for every user, asking for administrator
	// rights; otherwise for the current user without asking.
	PerMachine bool
	// DesktopShortcut adds a desktop shortcut.
	DesktopShortcut bool
	// ContextMenu are Explorer context menu entries to register, and
	// remove on uninstall.
	ContextMenu []Verb
	// Firewall adds an inbound Windows Firewall rule allowing the app on
	// private and domain networks (FirewallPublic adds public ones), so
	// devices on the network reach it without Windows' first-run prompt,
	// and removes it on uninstall. A per-user installer asks for
	// administrator rights for this step alone; declining skips the rule.
	Firewall, FirewallPublic bool
	// Output is the setup program to write.
	Output string
	// Verbose prints the makensis command and its output.
	Verbose bool
}

// FileType is a file type the app opens: its ProgID (a class of its
// own, "com.example.player.mkv"), description, extensions with the dot
// and an optional icon (.ico).
type FileType struct {
	ProgID, Name string
	Extensions   []string
	Icon         string
}

// Verb is an Explorer context menu entry: a key under
// Software\Classes (*\shell\name, Directory\shell\name, ...), its
// label, and the giode context action and path argument (%1, %V) its
// command passes to the app.
type Verb struct {
	Key, Label, ID, Arg string
	// CLSID, when set, makes it a DropTarget verb for that COM class,
	// served by the app (the giode library registers it): Explorer
	// hands the whole selection to one launch instead of starting the
	// app once per item.
	CLSID string
}

// Build writes a setup program with makensis: a wizard (welcome,
// license, folder, progress, finish with "Run"), Start menu and optional
// desktop shortcuts, an entry in Settings > Apps with an uninstaller,
// and per-user or per-machine install.
func Build(o Options) error {
	makensis, err := findMakensis()
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "giode-nsis-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	d := scriptData{
		Options: o,
		ExeName: filepath.Base(o.Exe),
		Exe:     abs(o.Exe),
		Output:  abs(o.Output),
	}
	if o.Icon != "" {
		d.IconFile = filepath.Join(work, "app.ico")
		if err := writeICO(o.Icon, d.IconFile); err != nil {
			return fmt.Errorf("installer icon: %w", err)
		}
	}
	if o.License != "" {
		d.License = abs(o.License)
	}
	for _, f := range append([]string{o.Exe}, o.Files...) {
		if st, err := os.Stat(f); err == nil {
			d.SizeKB += (st.Size() + 1023) / 1024
		}
	}
	var script bytes.Buffer
	if err := nsisTemplate.Execute(&script, d); err != nil {
		return err
	}
	nsi := filepath.Join(work, "setup.nsi")
	if err := os.WriteFile(nsi, script.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.Output), 0o755); err != nil {
		return err
	}
	cmd := exec.Command(makensis, "-V2", "-INPUTCHARSET", "UTF8", nsi)
	if o.Verbose {
		fmt.Fprintln(os.Stderr, "+", strings.Join(cmd.Args, " "))
	}
	done := progress.Command(cmd)
	out, err := cmd.CombinedOutput()
	done(err)
	if o.Verbose {
		os.Stderr.Write(out)
	}
	if err != nil {
		return fmt.Errorf("makensis: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Script returns the NSIS script Build would compile, for tests and
// for customizing.
func Script(o Options) (string, error) {
	var b bytes.Buffer
	err := nsisTemplate.Execute(&b, scriptData{Options: o, ExeName: filepath.Base(o.Exe), Exe: o.Exe, Output: o.Output, IconFile: o.Icon, License: o.License})
	return b.String(), err
}

func abs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// findMakensis finds NSIS's compiler on the PATH or in its Windows
// install folder.
func findMakensis() (string, error) {
	if p, err := exec.LookPath("makensis"); err == nil {
		return p, nil
	}
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
			p := filepath.Join(root, "NSIS", "makensis.exe")
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", errors.New("building a Windows installer needs NSIS: winget install NSIS.NSIS, brew install makensis, or apt install nsis")
}

// writeICO converts a PNG to a Windows icon with the usual sizes, each
// stored as PNG (supported since Windows Vista).
func writeICO(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return err
	}
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	var images [][]byte
	for _, s := range sizes {
		dstImg := image.NewNRGBA(image.Rect(0, 0, s, s))
		draw.CatmullRom.Scale(dstImg, dstImg.Bounds(), img, img.Bounds(), draw.Src, nil)
		var b bytes.Buffer
		if err := png.Encode(&b, dstImg); err != nil {
			return err
		}
		images = append(images, b.Bytes())
	}
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s)
		if s >= 256 {
			dim = 0 // 0 means 256
		}
		binary.Write(&out, binary.LittleEndian, struct {
			W, H, Colors, Reserved uint8
			Planes, BitCount       uint16
			Size, Offset           uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(images[i])), uint32(offset)})
		offset += len(images[i])
	}
	for _, b := range images {
		out.Write(b)
	}
	return os.WriteFile(dst, out.Bytes(), 0o644)
}

type scriptData struct {
	Options
	ExeName, Exe, Output, IconFile, License string
	SizeKB                                  int64
}

// nsisTemplate is the setup script. NSIS strings are quoted with
// double quotes; the q function escapes them.
var nsisTemplate = template.Must(template.New("nsis").Funcs(template.FuncMap{
	"q": func(s string) string {
		s = strings.ReplaceAll(s, "$", "$$")
		s = strings.ReplaceAll(s, `"`, `$\"`)
		return `"` + s + `"`
	},
	// base is a file's name, escaped for inside a quoted string.
	"base": func(s string) string {
		s = filepath.Base(s)
		s = strings.ReplaceAll(s, "$", "$$")
		return strings.ReplaceAll(s, `"`, `$\"`)
	},
}).Parse(`Unicode true
ManifestDPIAware true
SetCompressor /SOLID lzma
!include "MUI2.nsh"
!include "x64.nsh"

!define APPNAME {{q .Name}}
!define APPID {{q .ID}}
!define EXE {{q .ExeName}}
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPID}"
{{- if .Firewall}}
!define FW_DELETE 'advfirewall firewall delete rule name="${APPNAME}" dir=in'
!define FW_ADD 'advfirewall firewall add rule name="${APPNAME}" dir=in action=allow enable=yes profile={{if .FirewallPublic}}any{{else}}private,domain{{end}} program="$INSTDIR\${EXE}"'
{{- end}}

Name "${APPNAME}"
OutFile {{q .Output}}
BrandingText "${APPNAME} {{.Version}}"
{{- if .PerMachine}}
RequestExecutionLevel admin
InstallDir "$PROGRAMFILES64\${APPNAME}"
!define REGROOT HKLM
{{- else}}
RequestExecutionLevel user
InstallDir "$LOCALAPPDATA\Programs\${APPNAME}"
!define REGROOT HKCU
{{- end}}
InstallDirRegKey ${REGROOT} "${UNINST_KEY}" "InstallLocation"

VIProductVersion "{{.Version}}.0"
VIAddVersionKey "ProductName" "${APPNAME}"
VIAddVersionKey "ProductVersion" "{{.Version}}"
VIAddVersionKey "FileVersion" "{{.Version}}"
VIAddVersionKey "CompanyName" {{q .Publisher}}
VIAddVersionKey "FileDescription" "${APPNAME} Setup"
VIAddVersionKey "LegalCopyright" {{q .Publisher}}

{{- if .IconFile}}
!define MUI_ICON {{q .IconFile}}
!define MUI_UNICON {{q .IconFile}}
{{- end}}
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXE}"

!insertmacro MUI_PAGE_WELCOME
{{- if .License}}
!insertmacro MUI_PAGE_LICENSE {{q .License}}
{{- end}}
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
{{- if .PerMachine}}
	${If} ${RunningX64}
		SetRegView 64
	${EndIf}
	SetShellVarContext all
{{- else}}
	SetShellVarContext current
{{- end}}
FunctionEnd

Function un.onInit
{{- if .PerMachine}}
	${If} ${RunningX64}
		SetRegView 64
	${EndIf}
	SetShellVarContext all
{{- else}}
	SetShellVarContext current
{{- end}}
FunctionEnd

Section "Install"
	SetOutPath "$INSTDIR"
	File {{q .Exe}}
{{- range .Files}}
	File {{q .}}
{{- end}}
{{- range .FileTypes}}{{if .Icon}}
	File {{q .Icon}}
{{- end}}{{end}}
	WriteUninstaller "$INSTDIR\Uninstall.exe"
	CreateShortCut "$SMPROGRAMS\${APPNAME}.lnk" "$INSTDIR\${EXE}"
{{- if .DesktopShortcut}}
	CreateShortCut "$DESKTOP\${APPNAME}.lnk" "$INSTDIR\${EXE}"
{{- end}}
	WriteRegStr ${REGROOT} "${UNINST_KEY}" "DisplayName" "${APPNAME}"
	WriteRegStr ${REGROOT} "${UNINST_KEY}" "DisplayVersion" "{{.Version}}"
	WriteRegStr ${REGROOT} "${UNINST_KEY}" "Publisher" {{q .Publisher}}
{{- if .Website}}
	WriteRegStr ${REGROOT} "${UNINST_KEY}" "URLInfoAbout" {{q .Website}}
{{- end}}
	WriteRegStr ${REGROOT} "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${EXE}"
	WriteRegStr ${REGROOT} "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
	WriteRegStr ${REGROOT} "${UNINST_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
	WriteRegStr ${REGROOT} "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
	WriteRegDWORD ${REGROOT} "${UNINST_KEY}" "NoModify" 1
	WriteRegDWORD ${REGROOT} "${UNINST_KEY}" "NoRepair" 1
	WriteRegDWORD ${REGROOT} "${UNINST_KEY}" "EstimatedSize" {{.SizeKB}}
{{- range .FileTypes}}
	WriteRegStr ${REGROOT} {{q (printf "Software\\Classes\\%s" .ProgID)}} "" {{q .Name}}
	WriteRegStr ${REGROOT} {{q (printf "Software\\Classes\\%s\\DefaultIcon" .ProgID)}} "" {{if .Icon}}"$INSTDIR\{{base .Icon}}"{{else}}"$INSTDIR\${EXE},0"{{end}}
	WriteRegStr ${REGROOT} {{q (printf "Software\\Classes\\%s\\shell\\open\\command" .ProgID)}} "" '"$INSTDIR\${EXE}" "%1"'
{{- $id := .ProgID}}
{{- range .Extensions}}
	WriteRegStr ${REGROOT} {{q (printf "Software\\Classes\\%s\\OpenWithProgids" .)}} {{q $id}} ""
	WriteRegStr ${REGROOT} "Software\Classes\Applications\${EXE}\SupportedTypes" {{q .}} ""
{{- end}}
{{- end}}
{{- if .FileTypes}}
	System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, p 0, p 0)'
{{- end}}
{{- if .Firewall}}
	; An inbound firewall rule, replacing one an earlier install added.
{{- if .PerMachine}}
	nsExec::Exec 'netsh ${FW_DELETE}'
	Pop $0
	nsExec::Exec 'netsh ${FW_ADD}'
	Pop $0
{{- else}}
	ExecShellWait "runas" "netsh" '${FW_DELETE}' SW_HIDE
	ExecShellWait "runas" "netsh" '${FW_ADD}' SW_HIDE
{{- end}}
{{- end}}
{{- range .ContextMenu}}
	WriteRegStr ${REGROOT} {{q (printf "Software\\Classes\\%s" .Key)}} "MUIVerb" {{q .Label}}
	WriteRegStr ${REGROOT} {{q (printf "Software\\Classes\\%s" .Key)}} "Icon" "$INSTDIR\${EXE}"
	WriteRegStr ${REGROOT} {{q (printf "Software\\Classes\\%s\\command" .Key)}} "" '"$INSTDIR\${EXE}" --giode-action={{.ID}} "{{.Arg}}"'
{{- if .CLSID}}
	WriteRegStr ${REGROOT} {{q (printf "Software\\Classes\\%s" .Key)}} "MultiSelectModel" "Player"
	WriteRegStr ${REGROOT} {{q (printf "Software\\Classes\\%s\\DropTarget" .Key)}} "CLSID" {{q .CLSID}}
	WriteRegStr ${REGROOT} {{q (printf "Software\\Classes\\CLSID\\%s\\LocalServer32" .CLSID)}} "" '"$INSTDIR\${EXE}"'
{{- end}}
{{- end}}
SectionEnd

Section "Uninstall"
{{- if .Firewall}}
{{- if .PerMachine}}
	nsExec::Exec 'netsh ${FW_DELETE}'
	Pop $0
{{- else}}
	ExecShellWait "runas" "netsh" '${FW_DELETE}' SW_HIDE
{{- end}}
{{- end}}
	Delete "$INSTDIR\${EXE}"
{{- range .Files}}
	Delete "$INSTDIR\{{base .}}"
{{- end}}
	Delete "$INSTDIR\Uninstall.exe"
{{- range .FileTypes}}
	DeleteRegKey ${REGROOT} {{q (printf "Software\\Classes\\%s" .ProgID)}}
{{- $id := .ProgID}}
{{- range .Extensions}}
	DeleteRegValue ${REGROOT} {{q (printf "Software\\Classes\\%s\\OpenWithProgids" .)}} {{q $id}}
{{- end}}
{{- if .Icon}}
	Delete "$INSTDIR\{{base .Icon}}"
{{- end}}
{{- end}}
{{- if .FileTypes}}
	DeleteRegKey ${REGROOT} "Software\Classes\Applications\${EXE}"
{{- end}}
	RMDir "$INSTDIR"
	Delete "$SMPROGRAMS\${APPNAME}.lnk"
	Delete "$DESKTOP\${APPNAME}.lnk"
	DeleteRegKey ${REGROOT} "${UNINST_KEY}"
{{- range .ContextMenu}}
	DeleteRegKey ${REGROOT} {{q (printf "Software\\Classes\\%s" .Key)}}
{{- if .CLSID}}
	DeleteRegKey ${REGROOT} {{q (printf "Software\\Classes\\CLSID\\%s" .CLSID)}}
{{- end}}
{{- end}}
SectionEnd
`))
