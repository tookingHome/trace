//go:build windows

package main

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

var (
	shell32            = syscall.NewLazyDLL("shell32.dll")
	user32             = syscall.NewLazyDLL("user32.dll")
	procExtractIconExW = shell32.NewProc("ExtractIconExW")
	procPostMessageW   = user32.NewProc("PostMessageW")
)

const (
	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1
)

var iconApplied sync.Map // hwnd -> struct{}

// applyExeIcon sets the window's small/big icons from the executable resources
// so the taskbar and Alt-Tab show the TRACE icon (needed with custom chrome).
//
// Must use PostMessage (not SendMessage): Gio delivers ViewEvent while the
// window procedure is on the call stack; synchronous SendMessage(WM_SETICON)
// deadlocks the UI thread and leaves the app stuck on "加载中…".
func applyExeIcon(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	if _, seen := iconApplied.LoadOrStore(hwnd, struct{}{}); seen {
		return
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		iconApplied.Delete(hwnd)
		return
	}
	exe16, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		iconApplied.Delete(hwnd)
		return
	}
	var large, small uintptr
	r, _, _ := procExtractIconExW.Call(
		uintptr(unsafe.Pointer(exe16)),
		0,
		uintptr(unsafe.Pointer(&large)),
		uintptr(unsafe.Pointer(&small)),
		1,
	)
	if r == 0 {
		iconApplied.Delete(hwnd)
		return
	}
	if large != 0 {
		procPostMessageW.Call(hwnd, wmSetIcon, iconBig, large)
	}
	if small != 0 {
		procPostMessageW.Call(hwnd, wmSetIcon, iconSmall, small)
	}
}
