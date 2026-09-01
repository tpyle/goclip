//go:build !linux

package clipboard

import "testing"

func TestMaybeRunHolder_AlwaysFalse(t *testing.T) {
	if MaybeRunHolder([]string{"__goclip_holder__", "x11"}) {
		t.Fatal("MaybeRunHolder() = true on a non-Linux GOOS, want always false")
	}
	if MaybeRunHolder(nil) {
		t.Fatal("MaybeRunHolder(nil) = true, want false")
	}
}
