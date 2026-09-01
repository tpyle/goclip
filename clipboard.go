// Package clipboard copies text to the system clipboard without shelling
// out to any external program and without cgo.
//
// On Linux it talks directly to the X11 or Wayland display server (ICCCM
// selections, or the wlr-data-control-unstable-v1 protocol) and forks a
// short-lived helper process to hold the selection, exactly as tools like
// xclip/wl-copy do internally - see MaybeRunHolder. On Windows and macOS
// it calls the native clipboard APIs directly (both are OS services, so
// no helper process is needed there). See the README for platform notes
// and limitations.
package clipboard

import "errors"

// ErrUnsupportedPlatform is returned by Write when no display server (on
// Linux, neither $WAYLAND_DISPLAY nor $DISPLAY is set) or no native
// clipboard backend is available on the current GOOS.
var ErrUnsupportedPlatform = errors.New("clipboard: no supported display server or clipboard API available")
