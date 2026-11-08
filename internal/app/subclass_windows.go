//go:build windows

package app

import "unsafe"

// subclassFuncs loads comctl32 by name, so the activation context picks
// version 6, and returns SetWindowSubclass, DefSubclassProc and
// RemoveWindowSubclass, or zeros where it cannot.
func subclassFuncs() (set, def, remove uintptr) {
	lib, _, _ := procLoadLibraryExW.Call(uintptr(unsafe.Pointer(&u16z("comctl32.dll")[0])), 0, loadSystem32)
	if lib == 0 {
		return 0, 0, 0
	}
	proc := func(name string) uintptr {
		cname := append([]byte(name), 0)
		p, _, _ := procGetProcAddress.Call(lib, uintptr(unsafe.Pointer(&cname[0])))
		return p
	}
	return proc("SetWindowSubclass"), proc("DefSubclassProc"), proc("RemoveWindowSubclass")
}
