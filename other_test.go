//go:build !linux && !windows && !darwin

package clipboard

import (
	"errors"
	"testing"
)

func TestWrite_UnsupportedPlatform(t *testing.T) {
	if err := Write("secret"); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Write() err = %v, want ErrUnsupportedPlatform", err)
	}
}
