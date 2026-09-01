//go:build linux

package clipboard

import (
	"errors"
	"testing"
)

func TestSelectBackend(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    backendKind
		wantErr bool
	}{
		{
			name: "wayland takes precedence",
			env:  map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
			want: backendWayland,
		},
		{
			name: "wayland only",
			env:  map[string]string{"WAYLAND_DISPLAY": "wayland-0"},
			want: backendWayland,
		},
		{
			name: "x11 only",
			env:  map[string]string{"DISPLAY": ":0"},
			want: backendX11,
		},
		{
			name:    "neither set",
			env:     map[string]string{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := func(key string) string { return tt.env[key] }
			got, err := selectBackend(env)
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedPlatform) {
					t.Fatalf("selectBackend() err = %v, want ErrUnsupportedPlatform", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectBackend() unexpected err: %v", err)
			}
			if got != tt.want {
				t.Fatalf("selectBackend() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewBackend(t *testing.T) {
	if _, ok := newBackend(backendX11).(x11Backend); !ok {
		t.Error("newBackend(backendX11) did not return an x11Backend")
	}
	if _, ok := newBackend(backendWayland).(waylandBackend); !ok {
		t.Error("newBackend(backendWayland) did not return a waylandBackend")
	}
}
