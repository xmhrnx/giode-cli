// Package giopatch builds apps against a patched copy of Gio, for fixes
// and hooks Gio hasn't released: on Android, rendering at the display's
// refresh rate and keeping held keys repeating; everywhere, the hook
// behind giode.App's Config.Underlay. The copy lives in the user cache directory;
// the app's go.mod is left alone: the build gets a temporary copy of it
// with a replace directive, passed with -modfile to the go build and go
// list commands run in the app's module. (Not through GOFLAGS: go
// refuses -modfile in commands that run outside module mode.)
package giopatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// patch replaces old with new in a file of the Gio module, or, with
// old empty, adds the file with new as its content.
type patch struct {
	file, old, new string
}

// revision changes whenever the patches do, so cached copies made with
// older patches aren't used.
const revision = "9"

// androidPacing makes Android render at the display's refresh rate.
// Gio asks the Choreographer for the next frame only after presenting
// the current one; presenting blocks until a vsync, so the request
// misses the next one and a 120Hz display gets 60 frames a second.
// Asking before drawing catches it. A callback that arrives once the
// animation stopped is dropped instead of drawing a frame nobody asked
// for.
var androidPacing = []patch{
	{
		file: "app/os_android.go",
		old: `func (w *window) draw(env *C.JNIEnv, sync bool) {
	if !w.visible {
		return
	}
`,
		new: `func (w *window) draw(env *C.JNIEnv, sync bool) {
	if !w.visible {
		return
	}
	// giode: ask for the next frame before drawing this one, so the
	// request makes the next vsync.
	if !sync && !w.animating {
		return
	}
	wasAnimating := w.animating
	if wasAnimating {
		callVoidMethod(env, w.view, gioView.postFrameCallback)
	}
`,
	},
	{
		file: "app/os_android.go",
		old: `		Sync: sync,
	})
	if w.animating {
		callVoidMethod(env, w.view, gioView.postFrameCallback)
	}
`,
		new: `		Sync: sync,
	})
	if w.animating && !wasAnimating {
		callVoidMethod(env, w.view, gioView.postFrameCallback)
	}
`,
	},
}

// androidKeyRepeat keeps a held key repeating. After every edit Gio
// compares the snippet of text the keyboard was given with the
// editor's, and restarts the keyboard's input connection when they
// differ - which they do after any edit, because the editor moves its
// snippet window along with the text. Restarting cancels the
// keyboard's key repeat, so holding backspace deleted one character.
// The keyboard reads text on demand, so it only needs a restart when
// the text changed under it: unless one snippet lies within the other
// with the same text (the editor just moved its window).
var androidKeyRepeat = []patch{
	{
		file: "app/os_android.go",
		old: `		if old.Snippet != new.Snippet {
			callVoidMethod(env, w.view, gioView.restartInput)
			return
		}
`,
		new: `		if old.Snippet != new.Snippet && !snippetsAgree(old.Snippet, new.Snippet) {
			// giode: only when the text changed under the keyboard;
			// a restart cancels its key repeat.
			callVoidMethod(env, w.view, gioView.restartInput)
			return
		}
`,
	},
	{
		file: "app/os_android.go",
		old: `func (w *window) ShowTextInput(show bool) {
`,
		new: `// snippetsAgree reports whether one snippet of the editor's text lies
// within the other with the same text: the editor only moved its
// window over text the keyboard already knows (giode).
func snippetsAgree(a, b key.Snippet) bool {
	ar, br := []rune(a.Text), []rune(b.Text)
	if (len(ar) == 0) != (len(br) == 0) {
		return false
	}
	as, bs := min(a.Start, a.End), min(b.Start, b.End)
	start, end := max(as, bs), min(as+len(ar), bs+len(br))
	if end-start != min(len(ar), len(br)) {
		return false
	}
	return string(ar[start-as:end-as]) == string(br[start-bs:end-bs])
}

func (w *window) ShowTextInput(show bool) {
`,
	},
}

// underlay lets giode.App draw caller-rendered GPU content (video, 3D)
// under the UI: the window calls a hook with the frame's render target
// before drawing the UI over it without clearing, with Gio's OpenGL
// backend restoring the GL state it changes, and can be told to prefer
// OpenGL where it would pick Direct3D. giode uses app.GiodeSetUnderlay
// behind the giode_gio build tag, which builds against this copy set.
var underlay = []patch{
	{
		file: "app/window.go",
		old: `func (w *Window) frame(frame *op.Ops, viewport image.Point) error {
`,
		new: `func (w *Window) frame(frame *op.Ops, viewport image.Point) error {
	// giode: an underlay draws first, then the UI over it, uncleared.
	if f := giodeUnderlay; f != nil {
		target, err := w.ctx.RenderTarget()
		if err != nil {
			return err
		}
		f(giodeTarget(target, viewport))
		if err := w.gpu.Frame(frame, target, viewport); err != nil {
			return err
		}
		if f := giodeAfterFrame; f != nil {
			// The finished frame, before it's presented: read back for
			// screenshots.
			f(giodeTarget(target, viewport))
		}
		return nil
	}
`,
	},
	{
		file: "app/window.go",
		old: `	if w.gpu != nil {
		w.ctx.Lock()
		w.gpu.Release()
`,
		new: `	if w.gpu != nil {
		w.ctx.Lock()
		if f := giodeUnderlayRelease; f != nil {
			// giode: the underlay frees its GPU resources while the
			// context is current, before it's destroyed.
			f()
		}
		w.gpu.Release()
`,
	},
	{
		file: "app/window.go",
		old:  `gpu, err := gpu.New(w.ctx.API())`,
		new:  `gpu, err := gpu.New(giodeAPI(w.ctx.API()))`,
	},
	{
		file: "app/window.go",
		old: `		if !e2.Valid() && w.gpu != nil {
			w.ctx.Lock()
			w.gpu.Release()
`,
		new: `		if !e2.Valid() && w.gpu != nil {
			w.ctx.Lock()
			if f := giodeUnderlayRelease; f != nil {
				// giode: the view is going away (the window closing, an
				// Android surface destroyed).
				f()
			}
			w.gpu.Release()
`,
	},
	{
		file: "app/d3d11_windows.go",
		old: `		initializer: func(w *window) (context, error) {
			hwnd, _, _ := w.HWND()
`,
		new: `		initializer: func(w *window) (context, error) {
			if giodeForceGL {
				return nil, fmt.Errorf("giode: OpenGL requested")
			}
			hwnd, _, _ := w.HWND()
`,
	},
	{
		file: "gpu/internal/opengl/opengl.go",
		old: `	savedState glState
	sharedCtx  bool
`,
		new: `	savedState glState
	sharedCtx  bool
	// giode: the capabilities foreign GL code may leave on that Gio
	// assumes are off, and their state before the frame.
	giodeCaps [4]bool
`,
	},
	{
		file: "gpu/internal/opengl/opengl.go",
		old: `	if b.sharedCtx {
		b.glstate = b.queryState()
		b.savedState = b.glstate
	}
`,
		new: `	if b.sharedCtx {
		b.glstate = b.queryState()
		b.savedState = b.glstate
		for i, c := range giodeCapEnums {
			b.giodeCaps[i] = b.funcs.IsEnabled(c)
			if b.giodeCaps[i] {
				b.funcs.Disable(c)
			}
		}
	}
`,
	},
	{
		file: "gpu/internal/opengl/opengl.go",
		old: `	if b.sharedCtx {
		b.restoreState(b.savedState)
	}`,
		new: `	if b.sharedCtx {
		b.restoreState(b.savedState)
		for i, c := range giodeCapEnums {
			if b.giodeCaps[i] {
				b.funcs.Enable(c)
			}
		}
	}`,
	},
	{
		file: "gpu/internal/opengl/giode_caps.go",
		new: `// Added by giode's Gio patches.

package opengl

import "gioui.org/internal/gl"

// giodeCapEnums are GL_SCISSOR_TEST, GL_DEPTH_TEST, GL_STENCIL_TEST
// and GL_CULL_FACE: Gio never enables them, so a shared context's
// other user leaving one on would clip or drop Gio's drawing.
var giodeCapEnums = [4]gl.Enum{0x0C11, 0x0B71, 0x0B90, 0x0B44}
`,
	},
	{
		file: "app/giode_underlay.go",
		new: `// Added by giode's Gio patches: the hooks behind giode.App's
// Config.Underlay and Config.Renderer.

package app

import "gioui.org/gpu"

// GiodeTarget describes a frame's render target to an underlay.
type GiodeTarget struct {
	// API is 1 for OpenGL (ES), 2 Metal, 3 Direct3D 11, 4 Vulkan.
	API int
	// Framebuffer is the OpenGL framebuffer object, 0 for the window's.
	Framebuffer uint32
	// Handle is the MTLTexture, the ID3D11RenderTargetView or the VkImage.
	Handle        uintptr
	Width, Height int
}

var (
	giodeUnderlay        func(GiodeTarget)
	giodeForceGL         bool
	giodeUnderlayRelease func()
	giodeAfterFrame      func(GiodeTarget)
)

// GiodeSetUnderlay sets the function drawing under the UI each frame,
// with the GPU context current, and whether to prefer OpenGL over
// Direct3D. Call it before creating windows.
func GiodeSetUnderlay(f func(GiodeTarget), forceGL bool) {
	giodeUnderlay, giodeForceGL = f, forceGL
}

// GiodeSetUnderlayHooks sets the functions run on the render thread,
// with the GPU context current: release before Gio releases its GPU
// resources (the window closes, its view goes away, or the context is
// lost and made again), afterFrame once the UI is drawn over the underlay, before
// the frame is presented. Call it before creating windows.
func GiodeSetUnderlayHooks(release func(), afterFrame func(GiodeTarget)) {
	giodeUnderlayRelease, giodeAfterFrame = release, afterFrame
}

// giodeAPI makes Gio's OpenGL backend save and restore the GL state
// around its frames when an underlay shares the context.
func giodeAPI(api gpu.API) gpu.API {
	if gl, ok := api.(gpu.OpenGL); ok && giodeUnderlay != nil {
		gl.Shared = true
		return gl
	}
	return api
}
`,
	},
	{
		file: "app/giode_target.go",
		new: `//go:build !js

// Added by giode's Gio patches.

package app

import (
	"image"
	"unsafe"

	"gioui.org/gpu"
)

func giodeTarget(t gpu.RenderTarget, size image.Point) GiodeTarget {
	g := GiodeTarget{Width: size.X, Height: size.Y}
	switch t := t.(type) {
	case gpu.OpenGLRenderTarget:
		g.API, g.Framebuffer = 1, uint32(t.V)
	case gpu.MetalRenderTarget:
		g.API, g.Handle = 2, t.Texture
	case gpu.Direct3D11RenderTarget:
		g.API, g.Handle = 3, uintptr(unsafe.Pointer(t.RenderTarget))
	case gpu.VulkanRenderTarget:
		g.API, g.Handle = 4, uintptr(t.Image)
	}
	return g
}
`,
	},
	{
		file: "app/giode_target_js.go",
		new: `// Added by giode's Gio patches.

package app

import (
	"image"

	"gioui.org/gpu"
)

func giodeTarget(t gpu.RenderTarget, size image.Point) GiodeTarget {
	return GiodeTarget{API: 1, Width: size.X, Height: size.Y}
}
`,
	},
}

// windowsMinimized makes Windows report minimized windows: Gio reads
// the window's placement on every position change, but takes a
// minimized window for a restored one, so Minimized never arrives and
// restoring from it changes nothing either.
var windowsMinimized = []patch{
	{
		file: "app/os_windows.go",
		old: `	switch {
	case p.IsMaximized() && style&windows.WS_OVERLAPPEDWINDOW != 0:
		w.config.Mode = Maximized
`,
		new: `	switch {
	case p.IsMinimized():
		// giode: minimized, and restored from it.
		w.config.Mode = Minimized
	case p.IsMaximized() && style&windows.WS_OVERLAPPEDWINDOW != 0:
		w.config.Mode = Maximized
`,
	},
}

// fileDrag delivers files dragged onto a Linux window, behind giode's
// Config.OnFileDrop and OnFileDrag: XDND on X11, wl_data_device's drag
// and drop on Wayland. Both offer text/uri-list; the hook gets the
// drag's position in pixels and, on drop, the list.
var fileDrag = []patch{
	{
		file: "app/giode_drag.go",
		new: `// Added by giode's Gio patches: files dragged onto the window.

package app

// Kinds of GiodeSetFileDrag's events.
const (
	GiodeDragEnter = 1 + iota
	GiodeDragOver
	GiodeDragLeave
	GiodeDragDrop
)

var giodeDrag func(kind int, x, y float32, uris string)

// GiodeSetFileDrag sets the function receiving file drags on X11 and
// Wayland windows: enter, over and leave with the position in pixels,
// then drop with the files' text/uri-list. It's called on Gio's event
// goroutine, or (a drop on Wayland) its own. Call it before creating
// windows: they accept drags only when it's set.
func GiodeSetFileDrag(f func(kind int, x, y float32, uris string)) {
	giodeDrag = f
}
`,
	},
	{
		file: "app/os_x11.go",
		old: `	wakeups chan struct{}
	handler x11EventHandler
`,
		new: `	wakeups chan struct{}
	handler x11EventHandler
	// giode: file drags (XDND).
	giodeXdnd giodeXdnd
`,
	},
	{
		file: "app/os_x11.go",
		old: `		case C.SelectionNotify:
			cevt := (*C.XSelectionEvent)(unsafe.Pointer(xev))
`,
		new: `		case C.SelectionNotify:
			cevt := (*C.XSelectionEvent)(unsafe.Pointer(xev))
			if w.giodeXdndSelection(unsafe.Pointer(cevt)) {
				break
			}
`,
	},
	{
		file: "app/os_x11.go",
		old: `			cevt := (*C.XClientMessageEvent)(unsafe.Pointer(xev))
			switch *(*C.long)(unsafe.Pointer(&cevt.data)) {
`,
		new: `			cevt := (*C.XClientMessageEvent)(unsafe.Pointer(xev))
			if w.giodeXdndMessage(unsafe.Pointer(cevt)) {
				break
			}
			switch *(*C.long)(unsafe.Pointer(&cevt.data)) {
`,
	},
	{
		file: "app/os_x11.go",
		old: `	// extensions
	C.XSetWMProtocols(dpy, win, &w.atoms.evDelWindow, 1)
`,
		new: `	// extensions
	C.XSetWMProtocols(dpy, win, &w.atoms.evDelWindow, 1)
	w.giodeXdndInit(unsafe.Pointer(dpy), uintptr(win))
`,
	},
	{
		file: "app/giode_xdnd_x11.go",
		new: `// Added by giode's Gio patches: XDND, file drags onto X11 windows.

//go:build ((linux && !android) || freebsd || openbsd) && !nox11

package app

/*
#cgo freebsd openbsd CFLAGS: -I/usr/X11R6/include -I/usr/local/include
#cgo freebsd openbsd LDFLAGS: -L/usr/X11R6/lib -L/usr/local/lib -lX11
#cgo linux pkg-config: x11

#include <stdlib.h>
#include <X11/Xlib.h>
#include <X11/Xatom.h>
*/
import "C"

import "unsafe"

// xdndVersion is the protocol version giode speaks.
const xdndVersion = 5

type giodeXdnd struct {
	dpy *C.Display
	win C.Window
	// source is the window dragging onto this one, 0 between drags.
	source  C.Window
	version int
	accept  bool
	entered bool
	x, y    float32
	atoms   struct {
		aware, enter, position, status, leave, drop, finished,
		selection, typeList, actionCopy, uriList, property C.Atom
	}
}

func (w *x11Window) giodeXdndInit(dpy unsafe.Pointer, win uintptr) {
	if giodeDrag == nil {
		return
	}
	d := &w.giodeXdnd
	d.dpy, d.win = (*C.Display)(dpy), C.Window(win)
	atom := func(name string) C.Atom {
		c := C.CString(name)
		defer C.free(unsafe.Pointer(c))
		return C.XInternAtom(d.dpy, c, C.False)
	}
	a := &d.atoms
	a.aware, a.enter, a.position, a.status = atom("XdndAware"), atom("XdndEnter"), atom("XdndPosition"), atom("XdndStatus")
	a.leave, a.drop, a.finished, a.selection = atom("XdndLeave"), atom("XdndDrop"), atom("XdndFinished"), atom("XdndSelection")
	a.typeList, a.actionCopy, a.uriList = atom("XdndTypeList"), atom("XdndActionCopy"), atom("text/uri-list")
	a.property = atom("GIODE_XDND")
	version := C.long(xdndVersion)
	C.XChangeProperty(d.dpy, d.win, a.aware, C.XA_ATOM, 32, C.PropModeReplace, (*C.uchar)(unsafe.Pointer(&version)), 1)
}

// giodeXdndMessage handles an XDND client message, reporting whether
// it was one.
func (w *x11Window) giodeXdndMessage(p unsafe.Pointer) bool {
	d := &w.giodeXdnd
	ev := (*C.XClientMessageEvent)(p)
	if d.atoms.aware == 0 || ev.format != 32 {
		return false
	}
	l := (*[5]C.long)(unsafe.Pointer(&ev.data))
	a := &d.atoms
	switch ev.message_type {
	case a.enter:
		d.source, d.version, d.entered, d.accept = C.Window(l[0]), int(l[1]>>24), false, false
		types := []C.Atom{C.Atom(l[2]), C.Atom(l[3]), C.Atom(l[4])}
		if l[1]&1 != 0 {
			types = d.typeList()
		}
		for _, t := range types {
			if t == a.uriList {
				d.accept = true
			}
		}
	case a.position:
		if C.Window(l[0]) != d.source {
			return true
		}
		var x, y C.int
		var child C.Window
		C.XTranslateCoordinates(d.dpy, C.XDefaultRootWindow(d.dpy), d.win, C.int(l[2]>>16), C.int(l[2]&0xffff), &x, &y, &child)
		d.x, d.y = float32(x), float32(y)
		var status [5]C.long
		status[0] = C.long(d.win)
		if d.accept {
			kind := GiodeDragOver
			if !d.entered {
				d.entered, kind = true, GiodeDragEnter
			}
			giodeDrag(kind, d.x, d.y, "")
			status[1] = 1 | 2 // accepted; send every position
			status[4] = C.long(a.actionCopy)
		}
		d.send(a.status, status)
	case a.leave:
		if C.Window(l[0]) != d.source {
			return true
		}
		if d.entered {
			giodeDrag(GiodeDragLeave, d.x, d.y, "")
		}
		d.source, d.entered = 0, false
	case a.drop:
		if C.Window(l[0]) != d.source {
			return true
		}
		if !d.accept {
			d.finish(false)
			return true
		}
		t := C.Time(C.CurrentTime)
		if d.version >= 1 {
			t = C.Time(l[2])
		}
		C.XConvertSelection(d.dpy, a.selection, a.uriList, a.property, d.win, t)
		C.XFlush(d.dpy)
	default:
		return false
	}
	return true
}

// giodeXdndSelection reads a dropped uri-list, reporting whether the
// event was the drop's.
func (w *x11Window) giodeXdndSelection(p unsafe.Pointer) bool {
	d := &w.giodeXdnd
	ev := (*C.XSelectionEvent)(p)
	if d.atoms.aware == 0 || ev.selection != d.atoms.selection {
		return false
	}
	var uris string
	if ev.property != C.None {
		var typ C.Atom
		var format C.int
		var n, after C.ulong
		var data *C.uchar
		if C.XGetWindowProperty(d.dpy, d.win, d.atoms.property, 0, 1<<24, C.True, C.AnyPropertyType, &typ, &format, &n, &after, &data) == C.Success && data != nil {
			if format == 8 {
				uris = C.GoStringN((*C.char)(unsafe.Pointer(data)), C.int(n))
			}
			C.XFree(unsafe.Pointer(data))
		}
	}
	if d.source != 0 {
		giodeDrag(GiodeDragDrop, d.x, d.y, uris)
		d.finish(uris != "")
	}
	return true
}

// typeList reads the source's XdndTypeList, for drags of more than
// three types.
func (d *giodeXdnd) typeList() []C.Atom {
	var typ C.Atom
	var format C.int
	var n, after C.ulong
	var data *C.uchar
	if C.XGetWindowProperty(d.dpy, d.source, d.atoms.typeList, 0, 1024, C.False, C.XA_ATOM, &typ, &format, &n, &after, &data) != C.Success || data == nil {
		return nil
	}
	defer C.XFree(unsafe.Pointer(data))
	if format != 32 {
		return nil
	}
	// Format 32 properties are arrays of C longs.
	longs := unsafe.Slice((*C.long)(unsafe.Pointer(data)), int(n))
	types := make([]C.Atom, len(longs))
	for i, v := range longs {
		types[i] = C.Atom(v)
	}
	return types
}

func (d *giodeXdnd) finish(ok bool) {
	var l [5]C.long
	l[0] = C.long(d.win)
	if ok {
		l[1] = 1
		l[2] = C.long(d.atoms.actionCopy)
	}
	d.send(d.atoms.finished, l)
	d.source, d.entered = 0, false
}

func (d *giodeXdnd) send(typ C.Atom, l [5]C.long) {
	var xev C.XEvent
	ev := (*C.XClientMessageEvent)(unsafe.Pointer(&xev))
	ev._type = C.ClientMessage
	ev.display = d.dpy
	ev.window = d.source
	ev.message_type = typ
	ev.format = 32
	*(*[5]C.long)(unsafe.Pointer(&ev.data)) = l
	C.XSendEvent(d.dpy, d.source, C.False, C.NoEventMask, &xev)
	C.XFlush(d.dpy)
}
`,
	},
	{
		file: "app/os_wayland.go",
		old: `	// content is the data belonging to source.
	content []byte
}
`,
		new: `	// content is the data belonging to source.
	content []byte

	// giode: the file drag over one of the windows.
	giodeDnd giodeDnd
}
`,
	},
	{
		file: "app/os_wayland.go",
		old: `		if o == s.clipboard {
			continue
		}
`,
		new: `		if o == s.clipboard || o == s.giodeDnd.offer {
			continue
		}
`,
	},
	{
		file: "app/os_wayland.go",
		old: `	s := callbackLoad(data).(*wlSeat)
	s.serial = serial
	s.flushOffers()
}
`,
		new: `	s := callbackLoad(data).(*wlSeat)
	s.serial = serial
	s.giodeDndEnter(serial, surf, x, y, id)
	s.flushOffers()
}
`,
	},
	{
		file: "app/os_wayland.go",
		old: `func gio_onDataOfferAction(data unsafe.Pointer, offer *C.struct_wl_data_offer, act C.uint32_t) {
}
`,
		new: `func gio_onDataOfferAction(data unsafe.Pointer, offer *C.struct_wl_data_offer, act C.uint32_t) {
	if s := callbackLoad(data).(*wlSeat); offer == s.giodeDnd.offer {
		s.giodeDnd.action = act
	}
}
`,
	},
	{
		file: "app/os_wayland.go",
		old: `func gio_onDataDeviceLeave(data unsafe.Pointer, dataDev *C.struct_wl_data_device) {
}
`,
		new: `func gio_onDataDeviceLeave(data unsafe.Pointer, dataDev *C.struct_wl_data_device) {
	callbackLoad(data).(*wlSeat).giodeDndLeave()
}
`,
	},
	{
		file: "app/os_wayland.go",
		old: `func gio_onDataDeviceMotion(data unsafe.Pointer, dataDev *C.struct_wl_data_device, t C.uint32_t, x, y C.wl_fixed_t) {
}
`,
		new: `func gio_onDataDeviceMotion(data unsafe.Pointer, dataDev *C.struct_wl_data_device, t C.uint32_t, x, y C.wl_fixed_t) {
	callbackLoad(data).(*wlSeat).giodeDndMotion(x, y)
}
`,
	},
	{
		file: "app/os_wayland.go",
		old: `func gio_onDataDeviceDrop(data unsafe.Pointer, dataDev *C.struct_wl_data_device) {
}
`,
		new: `func gio_onDataDeviceDrop(data unsafe.Pointer, dataDev *C.struct_wl_data_device) {
	callbackLoad(data).(*wlSeat).giodeDndDrop()
}
`,
	},
	{
		file: "app/giode_dnd_wayland.go",
		new: `// Added by giode's Gio patches: file drags onto Wayland windows.

//go:build ((linux && !android) || freebsd) && !nowayland

package app

/*
#cgo linux pkg-config: wayland-client
#cgo freebsd LDFLAGS: -lwayland-client
#cgo freebsd CFLAGS: -I/usr/local/include

#include <stdlib.h>
#include <wayland-client.h>
*/
import "C"

import (
	"io"
	"os"
	"time"
	"unsafe"
)

const uriListMime = "text/uri-list"

type giodeDnd struct {
	offer  *C.struct_wl_data_offer
	win    *window
	action C.uint32_t
	x, y   float32
}

func (s *wlSeat) giodeDndEnter(serial C.uint32_t, surf *C.struct_wl_surface, x, y C.wl_fixed_t, id *C.struct_wl_data_offer) {
	s.giodeDndLeave()
	if giodeDrag == nil || id == nil || surf == nil {
		return
	}
	v, _ := callbackMap.Load(unsafe.Pointer(surf))
	w, ok := v.(*window)
	if !ok {
		return
	}
	found := false
	for _, m := range s.offers[id] {
		if m == uriListMime {
			found = true
		}
	}
	if !found {
		C.wl_data_offer_accept(id, serial, nil)
		return
	}
	mime := C.CString(uriListMime)
	defer C.free(unsafe.Pointer(mime))
	C.wl_data_offer_accept(id, serial, mime)
	if C.wl_proxy_get_version((*C.struct_wl_proxy)(unsafe.Pointer(id))) >= 3 {
		C.wl_data_offer_set_actions(id, C.WL_DATA_DEVICE_MANAGER_DND_ACTION_COPY, C.WL_DATA_DEVICE_MANAGER_DND_ACTION_COPY)
	}
	s.giodeDnd = giodeDnd{offer: id, win: w}
	s.giodeDndMove(x, y)
	giodeDrag(GiodeDragEnter, s.giodeDnd.x, s.giodeDnd.y, "")
}

func (s *wlSeat) giodeDndMove(x, y C.wl_fixed_t) {
	scale := float32(s.giodeDnd.win.scale)
	s.giodeDnd.x, s.giodeDnd.y = fromFixed(x)*scale, fromFixed(y)*scale
}

func (s *wlSeat) giodeDndMotion(x, y C.wl_fixed_t) {
	if s.giodeDnd.offer == nil {
		return
	}
	s.giodeDndMove(x, y)
	giodeDrag(GiodeDragOver, s.giodeDnd.x, s.giodeDnd.y, "")
}

func (s *wlSeat) giodeDndLeave() {
	d := s.giodeDnd
	if d.offer == nil {
		return
	}
	s.giodeDnd = giodeDnd{}
	giodeDrag(GiodeDragLeave, d.x, d.y, "")
	delete(s.offers, d.offer)
	callbackDelete(unsafe.Pointer(d.offer))
	C.wl_data_offer_destroy(d.offer)
}

// giodeDndDrop reads the dropped uri-list on its own goroutine: the
// source writes it after Gio's event loop goes back to dispatching.
func (s *wlSeat) giodeDndDrop() {
	d := s.giodeDnd
	if d.offer == nil {
		return
	}
	s.giodeDnd = giodeDnd{}
	delete(s.offers, d.offer)
	r, w, err := os.Pipe()
	if err != nil {
		giodeDrag(GiodeDragLeave, d.x, d.y, "")
		return
	}
	mime := C.CString(uriListMime)
	C.wl_data_offer_receive(d.offer, mime, C.int(w.Fd()))
	C.free(unsafe.Pointer(mime))
	w.Close()
	C.wl_display_flush(s.disp.disp)
	finish := C.wl_proxy_get_version((*C.struct_wl_proxy)(unsafe.Pointer(d.offer))) >= 3 && d.action != 0
	go func() {
		defer r.Close()
		r.SetReadDeadline(time.Now().Add(10 * time.Second))
		data, _ := io.ReadAll(io.LimitReader(r, 16<<20))
		giodeDrag(GiodeDragDrop, d.x, d.y, string(data))
		if finish {
			C.wl_data_offer_finish(d.offer)
		}
		callbackDelete(unsafe.Pointer(d.offer))
		C.wl_data_offer_destroy(d.offer)
		C.wl_display_flush(s.disp.disp)
	}()
}
`,
	},
}

// windowsFullscreen makes a fullscreen Windows window a popup covering
// its monitor. Gio removed the frame and showed the window maximized,
// and Windows draws the taskbar over a maximized window, even one
// covering the whole screen. Leaving fullscreen restores the window's
// earlier place and state.
var windowsFullscreen = []patch{
	{
		file: "app/os_windows.go",
		old: `	frameDims image.Point
	loop      *eventLoop
}
`,
		new: `	frameDims image.Point
	loop      *eventLoop
	// giode: whether the window is fullscreen (a popup), and its
	// placement before.
	giodeFullscreen bool
	giodePlacement  windows.WindowPlacement
}

// giodeWSPopup is WS_POPUP.
const giodeWSPopup = 0x80000000
`,
	},
	{
		file: "app/os_windows.go",
		old: `	case p.IsMinimized():
		// giode: minimized, and restored from it.
		w.config.Mode = Minimized
`,
		new: `	case p.IsMinimized():
		// giode: minimized, and restored from it.
		w.config.Mode = Minimized
	case w.giodeFullscreen:
		// giode: a fullscreen window is a popup, not maximized.
		w.config.Mode = Fullscreen
`,
	},
	{
		file: "app/os_windows.go",
		old: `	case Fullscreen:
		swpStyle |= windows.SWP_NOMOVE | windows.SWP_NOSIZE
		showMode = windows.SW_SHOWMAXIMIZED
	}
`,
		new: `	case Fullscreen:
		// giode: a popup covering the monitor.
		if !w.giodeFullscreen {
			w.giodePlacement = windows.GetWindowPlacement(w.hwnd)
			w.giodeFullscreen = true
		}
		style = style&^windows.WS_MAXIMIZE | giodeWSPopup
		mi := windows.GetMonitorInfo(w.hwnd)
		x, y = mi.Monitor.Left, mi.Monitor.Top
		width, height = mi.Monitor.Right-mi.Monitor.Left, mi.Monitor.Bottom-mi.Monitor.Top
		swpStyle &^= windows.SWP_NOZORDER
		if !cnf.TopMost {
			hwndAfter = windows.HWND_TOP
		}
		showMode = windows.SW_SHOWNORMAL
	}
	giodeLeaving := w.giodeFullscreen && cnf.Mode != Fullscreen
	if giodeLeaving {
		style &^= giodeWSPopup
		w.giodeFullscreen = false
	}
`,
	},
	{
		file: "app/os_windows.go",
		old: `	windows.ShowWindow(w.hwnd, showMode)
}
`,
		new: `	windows.ShowWindow(w.hwnd, showMode)
	switch {
	case cnf.Mode == Fullscreen:
		// giode: again after ShowWindow, which puts a window that was
		// maximized back in its normal place.
		windows.SetWindowPos(w.hwnd, hwndAfter, x, y, width, height, swpStyle)
	case giodeLeaving && (cnf.Mode == Windowed || cnf.Mode == Maximized):
		// giode: back where, and as, the window was before.
		windows.SetWindowPlacement(w.hwnd, &w.giodePlacement)
		if cnf.Mode == Maximized {
			windows.ShowWindow(w.hwnd, windows.SW_SHOWMAXIMIZED)
		}
	}
}
`,
	},
}

// macosFullscreen fixes leaving (and entering) fullscreen on macOS.
// Configure read the window's style mask, toggled fullscreen - which
// starts AppKit's animated transition - and then wrote the mask it
// read back, so leaving fullscreen set the fullscreen bit again in the
// middle of leaving (AppKit allows only toggleFullScreen: to change
// it). It also resized the window during the transition. And the mode
// was read again only when the window drew, so once a transition
// ended a window that wasn't drawing (a paused player) kept reporting
// the old mode. Also, TopMost turned off left the window floating.
var macosFullscreen = []patch{
	{
		file: "app/os_macos.go",
		old: `	scale  float32
	config Config
`,
		new: `	scale  float32
	config Config
	// giode: a fullscreen transition is running.
	giodeFSTransition bool
`,
	},
	{
		file: "app/os_macos.go",
		old: `		w.setTitle(cnf.Title)
		if C.isWindowZoomed(window) == 0 {
			C.zoomWindow(window)
		}
`,
		new: `		w.setTitle(cnf.Title)
		// giode: not while leaving fullscreen.
		if !fullscreen && !w.giodeFSTransition && C.isWindowZoomed(window) == 0 {
			C.zoomWindow(window)
		}
`,
	},
	{
		file: "app/os_macos.go",
		old: `		w.setTitle(cnf.Title)
		w.config.Size = cnf.Size
		cnf.Size = cnf.Size.Div(int(screenScale))
		C.setSize(window, C.CGFloat(cnf.Size.X), C.CGFloat(cnf.Size.Y))
		w.config.MinSize = cnf.MinSize
		cnf.MinSize = cnf.MinSize.Div(int(screenScale))
		C.setMinSize(window, C.CGFloat(cnf.MinSize.X), C.CGFloat(cnf.MinSize.Y))
		w.config.MaxSize = cnf.MaxSize
		cnf.MaxSize = cnf.MaxSize.Div(int(screenScale))
		if cnf.MaxSize != (image.Point{}) {
			C.setMaxSize(window, C.CGFloat(cnf.MaxSize.X), C.CGFloat(cnf.MaxSize.Y))
		}
		if C.isWindowZoomed(window) != 0 {
			C.zoomWindow(window)
		}
`,
		new: `		w.setTitle(cnf.Title)
		// giode: the window isn't resized while it's fullscreen or
		// changing to or from it: AppKit restores its frame itself.
		resize := !fullscreen && !w.giodeFSTransition
		w.config.Size = cnf.Size
		cnf.Size = cnf.Size.Div(int(screenScale))
		if resize {
			C.setSize(window, C.CGFloat(cnf.Size.X), C.CGFloat(cnf.Size.Y))
		}
		w.config.MinSize = cnf.MinSize
		cnf.MinSize = cnf.MinSize.Div(int(screenScale))
		if resize {
			C.setMinSize(window, C.CGFloat(cnf.MinSize.X), C.CGFloat(cnf.MinSize.Y))
		}
		w.config.MaxSize = cnf.MaxSize
		cnf.MaxSize = cnf.MaxSize.Div(int(screenScale))
		if resize && cnf.MaxSize != (image.Point{}) {
			C.setMaxSize(window, C.CGFloat(cnf.MaxSize.X), C.CGFloat(cnf.MaxSize.Y))
		}
		if resize && C.isWindowZoomed(window) != 0 {
			C.zoomWindow(window)
		}
`,
	},
	{
		file: "app/os_macos.go",
		old: `	if cnf.TopMost {
		C.setWindowLevel(window, C.NSFloatingWindowLevel)
	}
	C.setWindowTitlebarAppearsTransparent(window, barTrans)
	C.setWindowTitleVisibility(window, titleVis)
	C.setWindowStyleMask(window, mask)
`,
		new: `	if cnf.TopMost {
		C.setWindowLevel(window, C.NSFloatingWindowLevel)
	} else {
		// giode: and back.
		C.setWindowLevel(window, C.NSNormalWindowLevel)
	}
	C.setWindowTitlebarAppearsTransparent(window, barTrans)
	C.setWindowTitleVisibility(window, titleVis)
	// giode: only toggleFullScreen: changes the fullscreen bit; keep
	// the one the window has now, after any toggle above.
	mask = mask&^C.NSWindowStyleMaskFullScreen | C.getWindowStyleMask(window)&C.NSWindowStyleMaskFullScreen
	C.setWindowStyleMask(window, mask)
`,
	},
	{
		file: "app/os_macos.m",
		old: `- (void)windowWillEnterFullScreen:(NSNotification *)notification {
	NSWindow *window = (NSWindow *)[notification object];
  GioView *view = (GioView *)window.contentView;
	gio_onDraw(view.handle);
}
- (void)windowWillExitFullScreen:(NSNotification *)notification {
	NSWindow *window = (NSWindow *)[notification object];
  GioView *view = (GioView *)window.contentView;
	gio_onDraw(view.handle);
}
`,
		new: `- (void)windowWillEnterFullScreen:(NSNotification *)notification {
	NSWindow *window = (NSWindow *)[notification object];
  GioView *view = (GioView *)window.contentView;
	gio_giodeFullscreenTransition(view.handle, 1);
	gio_onDraw(view.handle);
}
- (void)windowWillExitFullScreen:(NSNotification *)notification {
	NSWindow *window = (NSWindow *)[notification object];
  GioView *view = (GioView *)window.contentView;
	gio_giodeFullscreenTransition(view.handle, 1);
	gio_onDraw(view.handle);
}
// giode: the transition is over; drawing reads the mode again and
// reports it, even in a window that wasn't going to draw.
- (void)windowDidEnterFullScreen:(NSNotification *)notification {
	NSWindow *window = (NSWindow *)[notification object];
  GioView *view = (GioView *)window.contentView;
	gio_giodeFullscreenTransition(view.handle, 0);
	gio_onDraw(view.handle);
}
- (void)windowDidExitFullScreen:(NSNotification *)notification {
	NSWindow *window = (NSWindow *)[notification object];
  GioView *view = (GioView *)window.contentView;
	gio_giodeFullscreenTransition(view.handle, 0);
	gio_onDraw(view.handle);
}
- (void)windowDidFailToEnterFullScreen:(NSWindow *)window {
  GioView *view = (GioView *)window.contentView;
	gio_giodeFullscreenTransition(view.handle, 0);
	gio_onDraw(view.handle);
}
- (void)windowDidFailToExitFullScreen:(NSWindow *)window {
  GioView *view = (GioView *)window.contentView;
	gio_giodeFullscreenTransition(view.handle, 0);
	gio_onDraw(view.handle);
}
`,
	},
	{
		file: "app/giode_fullscreen_macos.go",
		new: `// Added by giode's Gio patches: fullscreen transitions on macOS.

//go:build darwin && !ios

package app

/*
#include <stdint.h>
*/
import "C"

//export gio_giodeFullscreenTransition
func gio_giodeFullscreenTransition(h C.uintptr_t, active C.int) {
	windowFor(h).giodeFSTransition = active != 0
}
`,
	},
}

// inject lets giode's MCP server deliver pointer and key events as the
// platform would, for its ui_pointer and ui_key tools.
var inject = []patch{
	{
		file: "app/giode_inject.go",
		new: `// Added by giode's Gio patches: input from automation.

package app

import "gioui.org/io/event"

// GiodeInject delivers e - a pointer.Event, key.Event or key.EditEvent,
// in window pixels - to the window's input as if the platform had, on
// the window's event thread, and waits for it to be handled. Don't
// call it from the goroutine handling the window's events.
func (w *Window) GiodeInject(e event.Event) {
	if w.driver == nil {
		return
	}
	w.Run(func() {
		if w.driver == nil {
			return
		}
		w.processEvent(e)
		w.Invalidate()
	})
}
`,
	},
}

// Android is the set of patches for Android builds.
var Android = append(append([]patch{}, androidPacing...), androidKeyRepeat...)

// All is every patch, for builds of any target: the Android ones only
// touch Android's files. Builds against it have the giode_gio tag (see
// Tag).
var All = append(append(append(append(append(append(append(append([]patch{}, Android...), underlay...), windowsMinimized...), windowsFullscreen...), macosFullscreen...), fileDrag...), inject...), windowsBackground...)

// windowsBackground fills a new Windows window with the app's background
// color until its first frame: Gio skips the window's background erase,
// so Windows showed the window white until then.
var windowsBackground = []patch{
	{
		file: "app/giode_background.go",
		new: `// Added by giode's Gio patches: a new window's color before its first
// frame.

package app

import "sync/atomic"

// giodeBackground is the color as a Windows COLORREF (0x00BBGGRR) with
// bit 24 set, or 0 for none.
var giodeBackground atomic.Uint32

// GiodeSetBackground sets the color windows show until their first frame
// is drawn, where Windows would show white; on false stops it, once the
// first frame is on screen, so later frames don't flicker through it.
func GiodeSetBackground(r, g, b uint8, on bool) {
	if !on {
		giodeBackground.Store(0)
		return
	}
	giodeBackground.Store(1<<24 | uint32(b)<<16 | uint32(g)<<8 | uint32(r))
}
`,
	},
	{
		file: "app/giode_background_windows.go",
		new: `// Added by giode's Gio patches: a new window's color before its first
// frame.

package app

import (
	"syscall"
	"unsafe"
)

var (
	giodeUser32           = syscall.NewLazyDLL("user32.dll")
	giodeGDI32            = syscall.NewLazyDLL("gdi32.dll")
	giodeGetClientRect    = giodeUser32.NewProc("GetClientRect")
	giodeFillRect         = giodeUser32.NewProc("FillRect")
	giodeCreateSolidBrush = giodeGDI32.NewProc("CreateSolidBrush")
	giodeDeleteObject     = giodeGDI32.NewProc("DeleteObject")
)

// giodeEraseBackground fills the window's client area through hdc with
// GiodeSetBackground's color, when one is set.
func giodeEraseBackground(hwnd, hdc uintptr) {
	c := giodeBackground.Load()
	if c == 0 || hdc == 0 {
		return
	}
	var r [4]int32
	giodeGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	brush, _, _ := giodeCreateSolidBrush.Call(uintptr(c & 0xffffff))
	if brush == 0 {
		return
	}
	giodeFillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), brush)
	giodeDeleteObject.Call(brush)
}
`,
	},
	{
		file: "app/os_windows.go",
		old: `	case windows.WM_ERASEBKGND:
		// Avoid flickering between GPU content and background color.
		return windows.TRUE`,
		new: `	case windows.WM_ERASEBKGND:
		// giode: until the first frame, the app's background color
		// rather than white.
		giodeEraseBackground(uintptr(hwnd), wParam)
		// Avoid flickering between GPU content and background color.
		return windows.TRUE`,
	},
}

// Tag is the build tag of builds against patched Gio, which giode uses
// to reach the patches' hooks.
const Tag = "giode_gio"

// Tag2 is the build tag of builds against patches with the underlay's
// release and after-frame hooks, Linux file drags and input injection
// (revision 5 on),
// for giode versions that use them; builds have Tag too.
const Tag2 = "giode_gio2"

// Tag3 is the build tag of builds against patches with
// GiodeSetBackground (revision 9 on), for giode versions that use it;
// builds have Tag and Tag2 too.
const Tag3 = "giode_gio3"

// ErrSkipped is returned, wrapped, when a module can't be patched:
// it replaces or vendors Gio itself, or uses a workspace.
var ErrSkipped = errors.New("not patched")

type module struct {
	Path, Version, Dir string
	Replace            *module
}

// Prepare makes a patched copy of the Gio module the app in dir uses
// and returns the go.mod to build with (go build -modfile), and a
// cleanup function removing it.
func Prepare(dir string, patches []patch) (modfile string, cleanup func(), err error) {
	if _, err := os.Stat(filepath.Join(dir, "vendor")); err == nil {
		return "", nil, fmt.Errorf("%w: the module vendors its dependencies", ErrSkipped)
	}
	if out, err := goCmd(dir, "env", "GOWORK"); err == nil && out != "" && out != "off" {
		return "", nil, fmt.Errorf("%w: the module is in a workspace (%s)", ErrSkipped, out)
	}
	gomodPath, err := goCmd(dir, "env", "GOMOD")
	if err != nil {
		return "", nil, err
	}
	if gomodPath == "" || gomodPath == os.DevNull {
		return "", nil, fmt.Errorf("%w: %s isn't built in module mode", ErrSkipped, dir)
	}
	out, err := goCmd(dir, "list", "-m", "-json", "gioui.org")
	if err != nil {
		return "", nil, err
	}
	var m module
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return "", nil, fmt.Errorf("reading go list output: %w", err)
	}
	if m.Replace != nil {
		return "", nil, fmt.Errorf("%w: go.mod replaces gioui.org", ErrSkipped)
	}
	if m.Dir == "" {
		if _, err := goCmd(dir, "mod", "download", "gioui.org"); err != nil {
			return "", nil, err
		}
		if out, err = goCmd(dir, "list", "-m", "-json", "gioui.org"); err != nil {
			return "", nil, err
		}
		if err := json.Unmarshal([]byte(out), &m); err != nil || m.Dir == "" {
			return "", nil, errors.New("can't find the Gio module's source")
		}
	}
	patched, err := patchedCopy(m, patches)
	if err != nil {
		return "", nil, err
	}

	tmp, err := os.MkdirTemp("", "giode-mod-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(tmp) }
	// The module root may be above dir, where giode.toml is.
	gomod, err := os.ReadFile(gomodPath)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	gomod = append(gomod, fmt.Sprintf("\n// Added by giode for this build.\nreplace gioui.org => %s\n", quote(patched))...)
	modfile = filepath.Join(tmp, "go.mod")
	if err := os.WriteFile(modfile, gomod, 0o644); err != nil {
		cleanup()
		return "", nil, err
	}
	if sum, err := os.ReadFile(filepath.Join(filepath.Dir(gomodPath), "go.sum")); err == nil {
		if err := os.WriteFile(filepath.Join(tmp, "go.sum"), sum, 0o644); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return modfile, cleanup, nil
}

// patchedCopy returns the directory of a patched copy of m, making it
// the first time.
func patchedCopy(m module, patches []patch) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(cache, "giode", "gio", m.Version+"-giode"+revision)
	if _, err := os.Stat(filepath.Join(dst, "go.mod")); err == nil {
		return dst, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dst), "tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(m.Dir, tmp); err != nil {
		return "", err
	}
	if err := apply(tmp, patches); err != nil {
		return "", fmt.Errorf("Gio %s: %w", m.Version, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		if _, serr := os.Stat(filepath.Join(dst, "go.mod")); serr == nil {
			return dst, nil // another build made it first
		}
		return "", err
	}
	return dst, nil
}

// apply applies patches to the module in dir.
func apply(dir string, patches []patch) error {
	for _, p := range patches {
		file := filepath.Join(dir, filepath.FromSlash(p.file))
		if p.old == "" {
			if _, err := os.Stat(file); err == nil {
				return fmt.Errorf("%w: %s exists already", ErrSkipped, p.file)
			}
			if err := os.WriteFile(file, []byte(p.new), 0o644); err != nil {
				return err
			}
			continue
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		s := string(b)
		if strings.Count(s, p.old) != 1 {
			return fmt.Errorf("%w: %s doesn't have the code the patch expects", ErrSkipped, p.file)
		}
		s = strings.Replace(s, p.old, p.new, 1)
		if err := os.WriteFile(file, []byte(s), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies the files of src into dst, writable (the module
// cache is read-only).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func quote(path string) string {
	if strings.ContainsAny(path, " \t\"'`") {
		return fmt.Sprintf("%q", path)
	}
	return path
}

func goCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("go %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Diff returns giode's patches to the Gio module the app in dir uses,
// as a unified diff with a/ and b/ prefixes (patch -p1 in the module's
// directory), and the module's version. Flathub builds use it: they
// build from upstream sources and patch files, without the giode CLI.
// It needs diff (GNU or BSD).
func Diff(dir string, patches []patch) (diff, version string, err error) {
	out, err := goCmd(dir, "list", "-m", "-json", "gioui.org")
	if err != nil {
		return "", "", err
	}
	var m module
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return "", "", fmt.Errorf("reading go list output: %w", err)
	}
	if m.Replace != nil {
		return "", "", fmt.Errorf("%w: go.mod replaces gioui.org", ErrSkipped)
	}
	if m.Dir == "" {
		if _, err := goCmd(dir, "mod", "download", "gioui.org"); err != nil {
			return "", "", err
		}
		if out, err = goCmd(dir, "list", "-m", "-json", "gioui.org"); err != nil {
			return "", "", err
		}
		if err := json.Unmarshal([]byte(out), &m); err != nil || m.Dir == "" {
			return "", "", errors.New("can't find the Gio module's source")
		}
	}
	patched, err := patchedCopy(m, patches)
	if err != nil {
		return "", "", err
	}
	// diff names files by the paths it's given: run it on a and b.
	tmp, err := os.MkdirTemp("", "giode-diff-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)
	if err := os.Symlink(m.Dir, filepath.Join(tmp, "a")); err != nil {
		return "", "", err
	}
	if err := os.Symlink(patched, filepath.Join(tmp, "b")); err != nil {
		return "", "", err
	}
	cmd := exec.Command("diff", "-ruN", "a", "b")
	cmd.Dir = tmp
	b, err := cmd.Output()
	var ee *exec.ExitError
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 1) {
		return "", "", fmt.Errorf("diff: %w", err)
	}
	return string(b), m.Version, nil
}
