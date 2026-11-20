package msix

import (
	"crypto/sha1"
	"fmt"
	"html"
)

// ToastCLSID is the COM class of the toast activator of the app with the
// given ID: giode/notification registers the same class, so Windows
// hands it clicks on the app's toasts and their buttons, starting the
// app when needed.
func ToastCLSID(appID string) string {
	h := sha1.Sum([]byte("giode:toast:" + appID))
	h[6] = h[6]&0x0f | 0x50
	h[8] = h[8]&0x3f | 0x80
	return fmt.Sprintf("%08X-%04X-%04X-%04X-%012X", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// toastExtensions declare the toast activator: its class, served by the
// app's own executable.
func toastExtensions(a App, exe string) string {
	if a.ToastActivator == "" {
		return ""
	}
	e := html.EscapeString
	return fmt.Sprintf("\n        <desktop:Extension Category=\"windows.toastNotificationActivation\"><desktop:ToastNotificationActivation ToastActivatorCLSID=\"%s\"/></desktop:Extension>"+
		"\n        <com:Extension Category=\"windows.comServer\"><com:ComServer><com:ExeServer Executable=\"%s\" Arguments=\"-ToastActivated\" DisplayName=\"%s\"><com:Class Id=\"%s\" DisplayName=\"%s\"/></com:ExeServer></com:ComServer></com:Extension>",
		a.ToastActivator, e(exe), e(a.Name), a.ToastActivator, e(a.Name))
}
