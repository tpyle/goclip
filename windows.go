//go:build windows

package clipboard

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	openClipboard    = user32.NewProc("OpenClipboard")
	closeClipboard   = user32.NewProc("CloseClipboard")
	emptyClipboard   = user32.NewProc("EmptyClipboard")
	setClipboardData = user32.NewProc("SetClipboardData")

	kernel32     = windows.NewLazySystemDLL("kernel32.dll")
	globalAlloc  = kernel32.NewProc("GlobalAlloc")
	globalFree   = kernel32.NewProc("GlobalFree")
	globalLock   = kernel32.NewProc("GlobalLock")
	globalUnlock = kernel32.NewProc("GlobalUnlock")
	lstrcpy      = kernel32.NewProc("lstrcpyW")
)

// Write copies text to the system clipboard via the native Win32
// clipboard API (user32.dll/kernel32.dll, loaded from %SystemRoot%\
// System32 only via NewLazySystemDLL). No external process, and no
// helper process either: unlike X11/Wayland, Windows's clipboard is an
// OS service that copies the data at set-time rather than a selection an
// owner process must keep serving.
func Write(text string) error {
	// OpenClipboard/CloseClipboard must happen on the same OS thread, or
	// Windows treats the pair as belonging to two different clipboard
	// sessions and deadlocks.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := waitOpenClipboard(); err != nil {
		return err
	}
	defer closeClipboard.Call() //nolint:errcheck // best-effort cleanup; the operation's own error is more useful than a close failure

	if r, _, err := emptyClipboard.Call(0); r == 0 {
		return fmt.Errorf("clipboard: EmptyClipboard: %w", err)
	}

	data, err := syscall.UTF16FromString(text)
	if err != nil {
		return fmt.Errorf("clipboard: encode text as UTF-16: %w", err)
	}
	size := uintptr(len(data)) * unsafe.Sizeof(data[0])
	// The handle must be GMEM_MOVEABLE - SetClipboardData requires it.
	h, _, err := globalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return fmt.Errorf("clipboard: GlobalAlloc: %w", err)
	}

	l, _, err := globalLock.Call(h)
	if l == 0 {
		globalFree.Call(h) //nolint:errcheck // best-effort cleanup on an already-failing path
		return fmt.Errorf("clipboard: GlobalLock: %w", err)
	}
	if r, _, err := lstrcpy.Call(l, uintptr(unsafe.Pointer(&data[0]))); r == 0 { //nolint:gosec // G103: required to pass the UTF-16 buffer's address to lstrcpyW
		globalUnlock.Call(h) //nolint:errcheck // best-effort cleanup on an already-failing path
		globalFree.Call(h)   //nolint:errcheck // best-effort cleanup on an already-failing path
		return fmt.Errorf("clipboard: lstrcpyW: %w", err)
	}
	globalUnlock.Call(h) //nolint:errcheck // the copy above already succeeded; nothing actionable if unlock reports an error

	if r, _, err := setClipboardData.Call(cfUnicodeText, h); r == 0 {
		globalFree.Call(h) //nolint:errcheck // best-effort cleanup on an already-failing path
		return fmt.Errorf("clipboard: SetClipboardData: %w", err)
	}
	// Ownership of h passes to the system clipboard once SetClipboardData
	// succeeds - it must not be freed here.
	return nil
}

// waitOpenClipboard opens the clipboard, retrying for up to a second -
// OpenClipboard fails if another process/window currently holds it.
func waitOpenClipboard() error {
	deadline := time.Now().Add(time.Second)
	var err error
	for time.Now().Before(deadline) {
		var r uintptr
		r, _, err = openClipboard.Call(0)
		if r != 0 {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("clipboard: OpenClipboard: %w", err)
}
