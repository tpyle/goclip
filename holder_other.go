//go:build !linux

package clipboard

// MaybeRunHolder always returns false on this platform. Only Linux needs
// a persistent helper process to hold the clipboard selection open (see
// the linux build's MaybeRunHolder) - Windows and macOS clipboards are OS
// services with no such requirement, so callers can call this
// unconditionally on every platform without it ever doing anything here.
func MaybeRunHolder(_ []string) bool {
	return false
}
