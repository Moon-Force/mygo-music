//go:build windows

package app

import (
	"bytes"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"log"
	"strings"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo/ui"
)

// thumbbar puts the player's controls under the window's preview on the
// taskbar: ITaskbarList3's thumbnail buttons, which MyGo does not cover.
// Everything here runs on the main thread, where the framework has COM
// initialized and the common controls' version 6 active; the buttons'
// clicks come to the window as WM_COMMAND, which a subclass takes before
// the framework's own procedure.
type thumbbar struct {
	a    *app
	hwnd uintptr
	tl   uintptr // ITaskbarList3, or 0 once creation has failed

	setSubclass, defSubclass, removeSubclass uintptr // comctl32, version 6
	proc                                     uintptr // the subclass callback, made once
	buttonCreated                            uint32  // TaskbarButtonCreated's message number

	icons [4]uintptr // prev, play, pause, next; the shell keeps its own copies
	// offAppearance unlistens the tray's appearance changes.
	offAppearance func()

	added   bool // the buttons are on the preview
	enabled bool // the settings keep them
	playing bool // the middle button's icon as last set
	dead    bool
}

// ITaskbarList3's methods, counted from IUnknown's three: ITaskbarList
// takes 3-7 (HrInit at 3), ITaskbarList2's MarkFullscreenWindow 8, and
// ITaskbarList3 starts at SetProgressValue 9 and SetProgressState 10, as
// MyGo's own taskbar counts them.
const (
	tbRelease               = 2
	tbThumbBarAddButtons    = 15
	tbThumbBarUpdateButtons = 16
)

const (
	thbIcon    = 0x2
	thbTooltip = 0x4
	thbFlags   = 0x8

	thbfEnabled = 0x0
	thbfHidden  = 0x8

	thumbPrev = 1
	thumbPlay = 2
	thumbNext = 3

	wmCommand     = 0x0111
	thbnClicked   = 0x1800
	smCxSmallIcon = 49
	loadSystem32  = 0x800
	clsctxInproc  = 1
)

// thumbButton is the shell's THUMBBUTTON: the C layout on amd64 and arm64,
// 552 bytes, the Icon pointer aligned at 16.
type thumbButton struct {
	Mask   uint32
	ID     uint32
	Bitmap uint32
	Icon   uintptr
	Tip    [260]uint16
	Flags  uint32
}

type comGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

func guidOf(s string) comGUID {
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		panic("thumbbar: bad GUID " + s)
	}
	var g comGUID
	g.Data1 = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	g.Data2 = uint16(b[4])<<8 | uint16(b[5])
	g.Data3 = uint16(b[6])<<8 | uint16(b[7])
	copy(g.Data4[:], b[8:])
	return g
}

var (
	clsidTaskbarList = guidOf("56fdf344-fd6d-11d0-958a-006097c9a090")
	iidITaskbarList3 = guidOf("ea1afb91-9e28-4b86-90e9-9e9f8a5eefaf")

	ole32    = syscall.NewLazyDLL("ole32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCoCreateInstance         = ole32.NewProc("CoCreateInstance")
	procRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon              = user32.NewProc("DestroyIcon")
	procGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	procGetSystemMetricsForDpi   = user32.NewProc("GetSystemMetricsForDpi")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procLoadLibraryExW           = kernel32.NewProc("LoadLibraryExW")
	procGetProcAddress           = kernel32.NewProc("GetProcAddress")
)

// comCall calls method i of the COM object obj. Pointers passed as
// uintptr stay valid until it returns.
//
//go:uintptrescapes
func comCall(obj uintptr, i int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(nativePtr(obj))
	fn := *(*uintptr)(unsafe.Add(nativePtr(vtbl), i*int(unsafe.Sizeof(uintptr(0)))))
	var a [8]uintptr
	a[0] = obj
	n := copy(a[1:], args)
	r, _, _ := syscall.SyscallN(fn, a[:n+1]...)
	return r
}

// nativePtr turns an address of memory not managed by Go into a pointer, as
// MyGo's own COM calls do, reinterpreting the bits rather than converting a
// uintptr the garbage collector would not keep.
func nativePtr(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

func comFailed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func u16z(s string) []uint16 {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return []uint16{0}
	}
	return u
}

// newThumbbar subclasses the main window for its taskbar preview's
// buttons, or returns nil where they cannot be: the app goes on without
// them.
func newThumbbar(a *app) *thumbbar {
	if a.win == nil {
		return nil
	}
	if unsafe.Sizeof(thumbButton{}) != 552 {
		log.Println("thumbbar: unexpected THUMBBUTTON layout")
		return nil
	}
	t := &thumbbar{a: a, hwnd: a.win.NativeHandle(), enabled: a.settings.ThumbButtons}

	t.setSubclass, t.defSubclass, t.removeSubclass = subclassFuncs()
	if t.setSubclass == 0 || t.defSubclass == 0 || t.removeSubclass == 0 {
		log.Println("thumbbar: comctl32 lacks the subclassing APIs")
		return nil
	}

	var tl uintptr
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidTaskbarList)), 0, clsctxInproc,
		uintptr(unsafe.Pointer(&iidITaskbarList3)), uintptr(unsafe.Pointer(&tl)))
	if comFailed(hr) || tl == 0 || comFailed(comCall(tl, 3)) { // HrInit
		log.Printf("thumbbar: no taskbar list (HRESULT %#08x)", uint32(hr))
		if tl != 0 {
			comCall(tl, tbRelease)
		}
		return nil
	}
	t.tl = tl

	created := u16z("TaskbarButtonCreated")
	msg, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(&created[0])))
	t.buttonCreated = uint32(msg)

	t.makeIcons()
	t.proc = syscall.NewCallback(t.windowProc)
	r, _, _ := syscall.SyscallN(t.setSubclass, t.hwnd, t.proc, 1, uintptr(unsafe.Pointer(t)))
	if r == 0 {
		log.Println("thumbbar: cannot subclass the window")
		t.destroy()
		return nil
	}
	if a.tray != nil {
		t.offAppearance = a.tray.OnAppearanceChange(func() {
			t.makeIcons()
			if t.added {
				t.update(t.buttons())
			}
		})
	}
	// The button may exist already, or come with TaskbarButtonCreated:
	// both paths add them.
	t.ensure()
	return t
}

// windowProc takes the thumbnail buttons' clicks and the shell's notice
// that the taskbar button was made anew (Explorer restarted, or the
// window hid and showed); the rest goes on to the framework's procedure.
func (t *thumbbar) windowProc(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	if t.dead {
		return t.def(hwnd, msg, wp, lp)
	}
	switch {
	case uint32(msg) == t.buttonCreated:
		t.added = false
		t.ensure()
	case msg == wmCommand && uintptr(uint16(wp>>16)) == thbnClicked:
		switch uint16(wp) {
		case thumbPrev:
			t.a.pl.Prev()
		case thumbPlay:
			t.a.pl.TogglePlay()
		case thumbNext:
			t.a.pl.Next()
		}
		t.a.redraw()
		return 0
	}
	return t.def(hwnd, msg, wp, lp)
}

func (t *thumbbar) def(hwnd, msg, wp, lp uintptr) uintptr {
	r, _, _ := syscall.SyscallN(t.defSubclass, hwnd, msg, wp, lp)
	return r
}

// buttons are the three, as the settings keep or hide them.
func (t *thumbbar) buttons() [3]thumbButton {
	flags := uint32(thbfEnabled)
	if !t.enabled {
		flags = thbfHidden
	}
	play := t.icons[1]
	if t.playing {
		play = t.icons[2]
	}
	var bs [3]thumbButton
	for i, b := range []struct {
		id   uint32
		icon uintptr
		tip  string
	}{
		{thumbPrev, t.icons[0], T("Previous")},
		{thumbPlay, play, T("Play/Pause")},
		{thumbNext, t.icons[3], T("Next")},
	} {
		bs[i] = thumbButton{Mask: thbIcon | thbTooltip | thbFlags, ID: b.id, Icon: b.icon, Flags: flags}
		copy(bs[i].Tip[:], u16z(b.tip))
	}
	return bs
}

// ensure adds the buttons once the shell's taskbar button exists; they
// cannot be removed afterwards, only hidden.
func (t *thumbbar) ensure() {
	if t.added || t.tl == 0 {
		return
	}
	bs := t.buttons()
	hr := comCall(t.tl, tbThumbBarAddButtons, t.hwnd, uintptr(len(bs)), uintptr(unsafe.Pointer(&bs[0])))
	if comFailed(hr) {
		// The button is not made yet: TaskbarButtonCreated tries again.
		return
	}
	t.added = true
	t.playing = t.a.pl.Playing()
}

// update sends the buttons' changes to the shell.
func (t *thumbbar) update(bs [3]thumbButton) {
	if t.tl == 0 || !t.added {
		return
	}
	comCall(t.tl, tbThumbBarUpdateButtons, t.hwnd, uintptr(len(bs)), uintptr(unsafe.Pointer(&bs[0])))
}

// setEnabled shows or hides the buttons as the settings say.
func (t *thumbbar) setEnabled(on bool) {
	if t.dead || t.enabled == on {
		return
	}
	t.enabled = on
	if !t.added {
		t.ensure()
		return
	}
	t.update(t.buttons())
}

// sync keeps the middle button's icon with the player, as the app redraws.
func (t *thumbbar) sync() {
	if t.dead || !t.added {
		return
	}
	playing := t.a.pl.Playing()
	if playing == t.playing {
		return
	}
	t.playing = playing
	icon := t.icons[1]
	if playing {
		icon = t.icons[2]
	}
	b := thumbButton{Mask: thbIcon, ID: thumbPlay, Icon: icon}
	comCall(t.tl, tbThumbBarUpdateButtons, t.hwnd, 1, uintptr(unsafe.Pointer(&b)))
}

// makeIcons draws the four icons for the taskbar's appearance, at the
// window's DPI. The shell copies them on Add and Update, so the old handles
// go once the new ones are made.
func (t *thumbbar) makeIcons() {
	px := 16
	if dpi, _, _ := procGetDpiForWindow.Call(t.hwnd); dpi != 0 {
		if n, _, _ := procGetSystemMetricsForDpi.Call(smCxSmallIcon, dpi); n != 0 {
			px = int(n)
		}
	} else if n, _, _ := procGetSystemMetrics.Call(smCxSmallIcon); n != 0 {
		px = int(n)
	}
	ink := color.NRGBA{R: 0x1d, G: 0x1d, B: 0x1f, A: 255}
	if t.a.tray != nil && t.a.tray.Dark() {
		ink = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	}
	old := t.icons
	for i, svg := range []*ui.SVG{iconPrev, iconPlay, iconPause, iconNext} {
		t.icons[i] = iconHandle(svg.Image(px, px, ink), px)
	}
	for _, h := range old {
		if h != 0 {
			procDestroyIcon.Call(h)
		}
	}
}

// iconHandle makes an HICON of a rendered image, as the tray's icon is
// made of its PNG.
func iconHandle(img *image.RGBA, px int) uintptr {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil || b.Len() == 0 {
		return 0
	}
	data := b.Bytes()
	h, _, _ := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x00030000,
		uintptr(px), uintptr(px), 0)
	return h
}

// destroy takes the subclass off and frees what the shell does not keep.
func (t *thumbbar) destroy() {
	if t.dead {
		return
	}
	t.dead = true
	if t.offAppearance != nil {
		t.offAppearance()
	}
	if t.proc != 0 {
		syscall.SyscallN(t.removeSubclass, t.hwnd, t.proc, 1)
	}
	if t.tl != 0 {
		comCall(t.tl, tbRelease)
		t.tl = 0
	}
	for _, h := range t.icons {
		if h != 0 {
			procDestroyIcon.Call(h)
		}
	}
	t.icons = [4]uintptr{}
}
