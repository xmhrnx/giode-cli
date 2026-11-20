package giopatch

import _ "embed"

// TagWindow is the build tag of builds against patches with
// app.GiodeSetCloseRequest, GiodeSetTransparent, Window.GiodeSetHidden and
// Window.GiodeActivate (revision 23 on).
const TagWindow = "giode_gio8"

// The xdg-activation protocol's client code, made by wayland-scanner
// 1.22 from wayland-protocols' staging/xdg-activation/xdg-activation-v1.xml.
var (
	//go:embed files/wayland_xdg_activation.h
	xdgActivationHeader string
	//go:embed files/wayland_xdg_activation.c
	xdgActivationCode string
)

// linuxWindow gives Linux windows what Windows and macOS windows have:
//
//   - The app decides on a close the window manager asks for (X11's
//     WM_DELETE_WINDOW, Wayland's toplevel close): keeping the window open
//     to ask about unsaved work, or hiding it to the tray. The app's own
//     close goes ahead.
//   - Transparent windows on Wayland: no opaque region, and an EGL
//     configuration with alpha.
//   - Hiding a Wayland window, by unmapping it (a null buffer), and showing
//     it again with a fresh initial commit.
//   - Raising a Wayland window with xdg-activation, from a token the app
//     was launched or handed with, or one it asks for itself.
var linuxWindow = []patch{
	{
		file: "app/giode_window.go",
		new: `// Added by giode's Gio patches: closing, hiding, raising and
// transparent windows on Linux.

package app

import "sync/atomic"

var (
	giodeCloseRequest atomic.Pointer[func() bool]
	// giodeSelfClose is set while the app closes its window itself, which
	// X11 does by sending itself the window manager's close message.
	giodeSelfClose atomic.Bool
	giodeTransparent atomic.Bool
)

// GiodeSetCloseRequest sets the function that decides whether a close the
// window manager asks for (its close button) goes ahead, on X11 and
// Wayland. It runs on the window's event goroutine: it must be quick.
func GiodeSetCloseRequest(f func() bool) { giodeCloseRequest.Store(&f) }

func giodeAllowClose() bool {
	if giodeSelfClose.Load() {
		return true
	}
	if p := giodeCloseRequest.Load(); p != nil && *p != nil {
		return (*p)()
	}
	return true
}

// GiodeSetTransparent makes windows created after it transparent where
// they're drawn transparent, on Wayland.
func GiodeSetTransparent(on bool) { giodeTransparent.Store(on) }

type giodeHider interface{ giodeSetHidden(hide bool) bool }

// GiodeSetHidden hides or shows the window on Wayland, reporting whether
// it could. Call it from the window's event goroutine.
func (w *Window) GiodeSetHidden(hide bool) bool {
	ok := false
	w.Run(func() {
		if h, is := w.driver.(giodeHider); is {
			ok = h.giodeSetHidden(hide)
		}
	})
	return ok
}

type giodeActivator interface{ giodeActivate(token string) bool }

// GiodeActivate raises and focuses the window on Wayland with
// xdg-activation: with token (from XDG_ACTIVATION_TOKEN, or handed over by
// a tray or another instance), or with one the window asks for when token
// is empty. It reports whether the compositor has the protocol.
func (w *Window) GiodeActivate(token string) bool {
	ok := false
	w.Run(func() {
		if a, is := w.driver.(giodeActivator); is {
			ok = a.giodeActivate(token)
		}
	})
	return ok
}
`,
	},
	{
		file: "app/os_x11.go",
		old: `			case C.long(w.atoms.evDelWindow):
				w.shutdown(nil)
				return false`,
		new: `			case C.long(w.atoms.evDelWindow):
				// giode: the app may keep the window (GiodeSetCloseRequest).
				if !giodeAllowClose() {
					break
				}
				w.shutdown(nil)
				return false`,
	},
	{
		file: "app/os_x11.go",
		old: `func (w *x11Window) close() {
	var xev C.XEvent`,
		new: `func (w *x11Window) close() {
	giodeSelfClose.Store(true)
	var xev C.XEvent`,
	},
	{
		file: "app/os_wayland.go",
		old: `func gio_onToplevelClose(data unsafe.Pointer, topLvl *C.struct_xdg_toplevel) {
	w := callbackLoad(data).(*window)
	w.closing = true
}`,
		new: `func gio_onToplevelClose(data unsafe.Pointer, topLvl *C.struct_xdg_toplevel) {
	w := callbackLoad(data).(*window)
	// giode: the app may keep the window (GiodeSetCloseRequest).
	if giodeAllowClose() {
		w.closing = true
	}
}`,
	},
	{
		file: "app/os_wayland.go",
		old: `func (w *window) updateOpaqueRegion() {
`,
		new: `func (w *window) updateOpaqueRegion() {
	if giodeTransparent.Load() {
		// giode: a transparent window has no opaque part.
		C.wl_surface_set_opaque_region(w.surf, nil)
		return
	}
`,
	},
	{
		file: "app/egl_wayland.go",
		old: `		disp := egl.NativeDisplayType(unsafe.Pointer(w.display()))
		ctx, err := egl.NewContext(disp)`,
		new: `		disp := egl.NativeDisplayType(unsafe.Pointer(w.display()))
		egl.GiodeAlpha = giodeTransparent.Load()
		ctx, err := egl.NewContext(disp)`,
	},
	{
		file: "internal/egl/egl.go",
		old: `	if srgb {
		if runtime.GOOS == "linux" || runtime.GOOS == "android" {`,
		new: `	if srgb || GiodeAlpha {
		if runtime.GOOS == "linux" || runtime.GOOS == "android" {`,
	},
	{
		file: "internal/egl/giode_alpha.go",
		new: `// Added by giode's Gio patches.

package egl

// GiodeAlpha asks for a configuration with an alpha channel, for
// transparent Wayland windows.
var GiodeAlpha bool
`,
	},
	{
		file: "app/os_wayland.go",
		old: `#include "wayland_xdg_decoration.h"
`,
		new: `#include "wayland_xdg_decoration.h"
#include "wayland_xdg_activation.h"
`,
	},
	{
		file: "app/os_wayland.go",
		old:  `	case "xdg_wm_base":`,
		new: `	case "xdg_activation_v1":
		giodeActivation = (*C.struct_xdg_activation_v1)(C.wl_registry_bind(reg, name, &C.xdg_activation_v1_interface, 1))
	case "xdg_wm_base":`,
	},
	{
		file: "app/giode_wayland_egl.go",
		new: `// Added by giode's Gio patches.

//go:build ((linux && !android) || freebsd) && !nowayland && !noopengl

package app

// Present skips the frame of a hidden window: its buffer would map the
// window again.
func (c *wlContext) Present() error {
	if giodeWlHidden[c.win] {
		return nil
	}
	return c.Context.Present()
}
`,
	},
	{
		file: "app/vulkan_wayland.go",
		old: `func (c *wlVkContext) Present() error {
	return c.ctx.present()
}`,
		new: `func (c *wlVkContext) Present() error {
	if giodeWlHidden[c.win] {
		// giode: a hidden window's buffer would map it again.
		return nil
	}
	return c.ctx.present()
}`,
	},
	{file: "app/wayland_xdg_activation.h", new: xdgActivationHeader},
	{file: "app/wayland_xdg_activation.c", new: xdgActivationCode},
	{
		file: "app/giode_wayland.c",
		new: `// Added by giode's Gio patches.

//go:build ((linux && !android) || freebsd) && !nowayland

#include <wayland-client.h>
#include "wayland_xdg_shell.h"
#include "wayland_xdg_decoration.h"
#include "wayland_text_input.h"
#include "wayland_xdg_activation.h"
#include "_cgo_export.h"

const struct xdg_activation_token_v1_listener giode_activation_token_listener = {
	// Cast away the const of the token.
	.done = (void (*)(void *, struct xdg_activation_token_v1 *, const char *))giode_onActivationToken,
};
`,
	},
	{
		file: "app/giode_wayland.go",
		new: `// Added by giode's Gio patches: hiding and raising Wayland windows.

//go:build ((linux && !android) || freebsd) && !nowayland

package app

/*
#include <stdlib.h>
#include <wayland-client.h>
#include "wayland_xdg_shell.h"
#include "wayland_xdg_decoration.h"
#include "wayland_xdg_activation.h"

extern const struct xdg_activation_token_v1_listener giode_activation_token_listener;
extern const struct xdg_surface_listener gio_xdg_surface_listener;
extern const struct xdg_toplevel_listener gio_xdg_toplevel_listener;
extern const struct zxdg_toplevel_decoration_v1_listener gio_zxdg_toplevel_decoration_v1_listener;
*/
import "C"

import "unsafe"

// giodeActivation is the compositor's xdg_activation_v1, when it has one.
var giodeActivation *C.struct_xdg_activation_v1

// giodeWlHidden holds the hidden windows; used on the event goroutine.
var giodeWlHidden = map[*window]bool{}

// giodeSetHidden unmaps the window with a null buffer (its frames aren't
// presented while hidden), or maps it again with new role objects and
// their initial commit, to which the compositor answers with the
// configure that draws it. (The protocol lets the old ones commit again
// after a null buffer, but not every compositor answers that.)
func (w *window) giodeSetHidden(hide bool) bool {
	if w.surf == nil || hide == giodeWlHidden[w] {
		return true
	}
	if hide {
		giodeWlHidden[w] = true
		if w.lastFrameCallback != nil {
			C.wl_callback_destroy(w.lastFrameCallback)
			w.lastFrameCallback = nil
		}
		w.configured = false
		C.wl_surface_attach(w.surf, nil, 0, 0)
		C.wl_surface_commit(w.surf)
		C.wl_display_flush(w.disp.disp)
		return true
	}
	delete(giodeWlHidden, w)
	if w.decor != nil {
		C.zxdg_toplevel_decoration_v1_destroy(w.decor)
		w.decor = nil
	}
	C.xdg_toplevel_destroy(w.topLvl)
	C.xdg_surface_destroy(w.wmSurf)
	w.configured = false
	w.wmSurf = C.xdg_wm_base_get_xdg_surface(w.disp.wm, w.surf)
	w.topLvl = C.xdg_surface_get_toplevel(w.wmSurf)
	C.xdg_surface_add_listener(w.wmSurf, &C.gio_xdg_surface_listener, unsafe.Pointer(w.surf))
	C.xdg_toplevel_add_listener(w.topLvl, &C.gio_xdg_toplevel_listener, unsafe.Pointer(w.surf))
	id := C.CString(ID)
	C.xdg_toplevel_set_app_id(w.topLvl, id)
	C.free(unsafe.Pointer(id))
	if w.config.Title != "" {
		t := C.CString(w.config.Title)
		C.xdg_toplevel_set_title(w.topLvl, t)
		C.free(unsafe.Pointer(t))
	}
	w.setWindowConstraints()
	if w.disp.decor != nil {
		w.decor = C.zxdg_decoration_manager_v1_get_toplevel_decoration(w.disp.decor, w.topLvl)
		C.zxdg_toplevel_decoration_v1_add_listener(w.decor, &C.gio_zxdg_toplevel_decoration_v1_listener, unsafe.Pointer(w.surf))
	}
	C.wl_surface_commit(w.surf)
	C.wl_display_flush(w.disp.disp)
	return true
}

func (w *window) giodeActivate(token string) bool {
	if giodeActivation == nil || w.surf == nil {
		return false
	}
	if token != "" {
		t := C.CString(token)
		C.xdg_activation_v1_activate(giodeActivation, t, w.surf)
		C.free(unsafe.Pointer(t))
		C.wl_display_flush(w.disp.disp)
		return true
	}
	// Ask for a token tied to the latest input, which compositors honor
	// while the app has (or just had) the user's attention.
	tok := C.xdg_activation_v1_get_activation_token(giodeActivation)
	C.xdg_activation_token_v1_add_listener(tok, &C.giode_activation_token_listener, unsafe.Pointer(w.surf))
	if s := w.disp.seat; s != nil && s.serial != 0 {
		C.xdg_activation_token_v1_set_serial(tok, s.serial, s.seat)
	}
	C.xdg_activation_token_v1_set_surface(tok, w.surf)
	id := C.CString(ID)
	C.xdg_activation_token_v1_set_app_id(tok, id)
	C.free(unsafe.Pointer(id))
	C.xdg_activation_token_v1_commit(tok)
	C.wl_display_flush(w.disp.disp)
	return true
}

//export giode_onActivationToken
func giode_onActivationToken(data unsafe.Pointer, tok *C.struct_xdg_activation_token_v1, token *C.char) {
	if w, ok := callbackLoad(data).(*window); ok && giodeActivation != nil {
		C.xdg_activation_v1_activate(giodeActivation, token, w.surf)
	}
	C.xdg_activation_token_v1_destroy(tok)
}
`,
	},
}
