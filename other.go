//go:build !linux && !windows && !darwin

package clipboard

// Write always returns ErrUnsupportedPlatform - this package has no
// clipboard backend for this GOOS.
func Write(_ string) error {
	return ErrUnsupportedPlatform
}
