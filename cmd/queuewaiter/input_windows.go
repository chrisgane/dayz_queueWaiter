package main

import (
	"math/rand/v2"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	pSendInput                 = user32.NewProc("SendInput")
	pGetCursorPos              = user32.NewProc("GetCursorPos")
	pSetCursorPos              = user32.NewProc("SetCursorPos")
	pGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	pGetWindowTextW            = user32.NewProc("GetWindowTextW")
	pSetProcessDpiAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware        = user32.NewProc("SetProcessDPIAware")
)

type point struct{ X, Y int32 }

func cursorPos() point {
	var p point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p
}

// dpiAware makes screen coordinates real pixels, so the mouse position
// matches the screenshot on scaled (125%, 150%) displays.
func dpiAware() {
	const perMonitorAwareV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT -4
	if pSetProcessDpiAwarenessCtx.Find() == nil {
		if r, _, _ := pSetProcessDpiAwarenessCtx.Call(perMonitorAwareV2); r != 0 {
			return
		}
	}
	pSetProcessDPIAware.Call()
}

// dayzInFront reports whether the active window belongs to DayZ.
func dayzInFront() bool {
	title, exe := foreground()
	return strings.Contains(strings.ToLower(title), "dayz") || strings.HasPrefix(strings.ToLower(exe), "dayz")
}

// foreground returns the active window's title and program name. The
// program name can be empty: anti-cheat may not let other programs look at
// the game.
func foreground() (title, exe string) {
	hwnd, _, _ := pGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", ""
	}
	buf := make([]uint16, 256)
	n, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	title = windows.UTF16ToString(buf[:n])

	var pid uint32
	pGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return title, ""
	}
	defer windows.CloseHandle(h)
	path := make([]uint16, windows.MAX_PATH)
	size := uint32(len(path))
	if err := windows.QueryFullProcessImageName(h, 0, &path[0], &size); err != nil {
		return title, ""
	}
	return title, filepath.Base(windows.UTF16ToString(path[:size]))
}

// SendInput's INPUT, for the 64-bit layout.
type mouseInput struct {
	Type      uint32
	_         uint32
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type keyInput struct {
	Type      uint32
	_         uint32
	Vk, Scan  uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
	_         [8]byte
}

const (
	inputMouse    = 0
	inputKeyboard = 1
	mouseLeftDown = 0x0002
	mouseLeftUp   = 0x0004
	keyScanCode   = 0x0008
	keyUp         = 0x0002
	scanEnter     = 0x1C
)

func sendMouse(flags uint32) {
	in := mouseInput{Type: inputMouse, Flags: flags}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func sendKey(scan uint16, flags uint32) {
	in := keyInput{Type: inputKeyboard, Scan: scan, Flags: keyScanCode | flags}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func pause(lo, hi time.Duration) {
	time.Sleep(lo + time.Duration(rand.Int64N(int64(hi-lo)+1)))
}

// doubleClick double-clicks at p, at the speed a person would.
func doubleClick(p point) {
	pSetCursorPos.Call(uintptr(p.X), uintptr(p.Y))
	pause(20*time.Millisecond, 50*time.Millisecond)
	for i := range 2 {
		sendMouse(mouseLeftDown)
		pause(35*time.Millisecond, 75*time.Millisecond)
		sendMouse(mouseLeftUp)
		if i == 0 {
			pause(70*time.Millisecond, 130*time.Millisecond)
		}
	}
}

func pressEnter() {
	sendKey(scanEnter, 0)
	pause(45*time.Millisecond, 95*time.Millisecond)
	sendKey(scanEnter, keyUp)
}
