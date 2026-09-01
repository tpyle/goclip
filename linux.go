//go:build linux

package clipboard

import "os"

// Write copies text to the system clipboard. It requires an active X11 or
// Wayland session (see selectBackend) and forks a short-lived helper
// process to hold the selection - see MaybeRunHolder.
func Write(text string) error {
	kind, err := selectBackend(os.Getenv)
	if err != nil {
		return err
	}
	return spawnHolder(kind, text)
}

// backendKind identifies which display-server clipboard protocol to use.
type backendKind int

const (
	backendX11 backendKind = iota
	backendWayland
)

// selectBackend picks which display-server protocol to use, mirroring
// the precedence clipboard tools conventionally use: Wayland if
// $WAYLAND_DISPLAY is set, else X11 if $DISPLAY is set. It's a pure
// function of an injectable env lookup so it's testable without touching
// real environment variables.
func selectBackend(env func(string) string) (backendKind, error) {
	if env("WAYLAND_DISPLAY") != "" {
		return backendWayland, nil
	}
	if env("DISPLAY") != "" {
		return backendX11, nil
	}
	return 0, ErrUnsupportedPlatform
}

// backend implements ownership of one display-server clipboard protocol:
// take ownership of the selection, invoke ready once ownership is
// confirmed, then block serving paste requests with data until ownership
// is lost or the transport errors.
type backend interface {
	serve(data []byte, ready func()) error
}

// newBackend is a package-level var, not a plain function, so tests can
// substitute a fake backend instead of touching a real display server.
var newBackend = func(kind backendKind) backend {
	if kind == backendWayland {
		return waylandBackend{}
	}
	return x11Backend{}
}
