//go:build windows

package app

import (
	"log"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows/registry"
)

// trayPromoted reports whether the app's tray icon shows in the taskbar
// corner rather than hidden in the overflow area behind the chevron. A
// hidden icon still reports the chevron's rectangle from
// Shell_NotifyIconGetRect, so the rectangle alone cannot tell the two
// apart; Windows 11 records the visibility under NotifyIconSettings instead.
// Injectable in tests.
var trayPromoted = defaultTrayPromoted

// defaultTrayPromoted reads this executable's promotion flag. It fails
// open: where the flag cannot be read (older Windows, an icon not yet
// recorded, any error) it allows the panel, and only a recorded IsPromoted
// of 0 — the icon explicitly hidden in the overflow — suppresses it.
func defaultTrayPromoted() bool {
	exe, err := os.Executable()
	if err != nil {
		return true
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\NotifyIconSettings`, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return true // the key is absent before Windows 11
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return true
	}
	for _, name := range names {
		sub, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		path, _, errPath := sub.GetStringValue("ExecutablePath")
		if errPath != nil || !strings.EqualFold(path, exe) {
			sub.Close()
			continue
		}
		// The subkey names are a hash of the icon, so the executable's path
		// is what ties one to this app.
		promoted, _, errPromo := sub.GetIntegerValue("IsPromoted")
		sub.Close()
		if errPromo != nil {
			return true // recorded without a flag: leave it as it was
		}
		return promoted == 1
	}
	return true // this executable is not recorded yet
}

// The shell reports the pointer over a notification icon as a mouse move
// to the icon's window, which is the framework's hidden application
// window here; a subclass sees it before the framework's own procedure.
// These reports tell where an icon hidden in the overflow area really is,
// which Shell_NotifyIconGetRect reports as the chevron's rectangle.

// MyGo's callback message for its tray icons, and the mouse messages it
// carries for the pointer over one: uCallbackMessage before version 4
// holds the mouse message in lParam.
const (
	wmAppTray    = 0x8000 + 3
	wmMouseMove  = 0x0200
	wmMouseLeave = 0x02A3
)

var (
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetCurrentProcessId      = kernel32.NewProc("GetCurrentProcessId")
)

// appWindowClass is the class of the hidden window that receives the app's
// messages, the tray's among them.
const appWindowClass = "MyGoApp"

// hoverWatch takes the shell's reports of the pointer over the tray's
// icon. Everything here runs on the main thread, where the window lives.
type hoverWatch struct {
	h          *trayHover
	hwnd       uintptr
	proc       uintptr // the subclass callback, made once
	def, remov uintptr
	dead       bool
}

func (h *trayHover) listen() {
	if h.watch != nil {
		return
	}
	mygo.RunOnMain(func() {
		if h.stopped || h.watch != nil {
			return
		}
		h.watch = newHoverWatch(h)
	})
}

func (h *trayHover) unlisten() {
	h.hoverOn = false
	w := h.watch
	if w == nil {
		return
	}
	h.watch = nil
	mygo.RunOnMain(w.destroy)
}

func newHoverWatch(h *trayHover) *hoverWatch {
	set, def, remove := subclassFuncs()
	if set == 0 || def == 0 || remove == 0 {
		log.Println("tray hover: comctl32 lacks the subclassing APIs")
		return nil
	}
	hwnd := appWindow()
	if hwnd == 0 {
		log.Println("tray hover: cannot find the app's window")
		return nil
	}
	w := &hoverWatch{h: h, hwnd: hwnd, def: def, remov: remove}
	w.proc = syscall.NewCallback(w.windowProc)
	if r, _, _ := syscall.SyscallN(set, hwnd, w.proc, 1, uintptr(unsafe.Pointer(w))); r == 0 {
		log.Println("tray hover: cannot subclass the app's window")
		return nil
	}
	return w
}

// windowProc takes the shell's hover reports, and leaves the clicks and
// the framework's other messages to its procedure.
func (w *hoverWatch) windowProc(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	if !w.dead && uint32(msg) == wmAppTray {
		switch uint32(lp) {
		case wmMouseMove:
			w.h.hovered(cursorPoint())
		case wmMouseLeave:
			w.h.left()
		}
	}
	r, _, _ := syscall.SyscallN(w.def, hwnd, msg, wp, lp)
	return r
}

func (w *hoverWatch) destroy() {
	if w.dead {
		return
	}
	w.dead = true
	syscall.SyscallN(w.remov, w.hwnd, w.proc, 1)
}

// appWindowFound is the window the last lookup found, and appWindowProc
// the callback that looks: callbacks cannot close over what they find.
var appWindowFound uintptr

var appWindowProc = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
	if windowClass(hwnd) != appWindowClass {
		return 1
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	own, _, _ := procGetCurrentProcessId.Call()
	if uintptr(pid) != own {
		return 1
	}
	appWindowFound = hwnd
	return 0 // stop
})

// appWindow is the framework's hidden application window, or 0 where it
// cannot be found: the hover panel goes on without it.
func appWindow() uintptr {
	appWindowFound = 0
	procEnumWindows.Call(appWindowProc, 0)
	return appWindowFound
}

func windowClass(h uintptr) string {
	var buf [64]uint16
	n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}
