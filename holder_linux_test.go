//go:build linux

package clipboard

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestParseHolderArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantOK   bool
		wantKind backendKind
	}{
		{name: "not the sentinel", args: []string{"clip"}, wantOK: false},
		{name: "sentinel but no backend arg", args: []string{holderSentinel}, wantOK: false},
		{name: "x11", args: []string{holderSentinel, "x11"}, wantOK: true, wantKind: backendX11},
		{name: "wayland", args: []string{holderSentinel, "wayland"}, wantOK: true, wantKind: backendWayland},
		{name: "unrecognized backend defaults to x11", args: []string{holderSentinel, "bogus"}, wantOK: true, wantKind: backendX11},
		{name: "empty", args: nil, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, ok := parseHolderArgs(tt.args)
			if ok != tt.wantOK {
				t.Fatalf("parseHolderArgs(%v) ok = %v, want %v", tt.args, ok, tt.wantOK)
			}
			if ok && kind != tt.wantKind {
				t.Fatalf("parseHolderArgs(%v) kind = %v, want %v", tt.args, kind, tt.wantKind)
			}
		})
	}
}

func TestMaybeRunHolder_NotHolderMode(t *testing.T) {
	if MaybeRunHolder([]string{"copy", "some/entry"}) {
		t.Fatal("MaybeRunHolder() = true for ordinary CLI args, want false")
	}
	if MaybeRunHolder(nil) {
		t.Fatal("MaybeRunHolder(nil) = true, want false")
	}
}

// runHolder itself (reading real fd 3/4, driving a real backend) is
// deliberately left to manual/integration verification alongside the
// real X11/Wayland backends: forcibly reassigning this test process's own
// fd 3/4 to fake them would risk clobbering fds the test binary or Go
// runtime already has open (e.g. a coverage data file), for a test that
// still wouldn't exercise anything beyond what parseHolderArgs,
// MaybeRunHolder's dispatch, and the backend interface already cover
// individually.

func TestBuildHolderCommand(t *testing.T) {
	secretR, secretW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = secretR.Close() }()
	defer func() { _ = secretW.Close() }()
	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readyR.Close() }()
	defer func() { _ = readyW.Close() }()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()

	cmd := buildHolderCommand("/usr/bin/self", backendWayland, secretR, readyW, devNull)

	if cmd.Path != "/usr/bin/self" {
		t.Errorf("Path = %q, want /usr/bin/self", cmd.Path)
	}
	wantArgs := []string{"/usr/bin/self", holderSentinel, "wayland"}
	if !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Errorf("Args = %v, want %v", cmd.Args, wantArgs)
	}
	if len(cmd.ExtraFiles) != 2 || cmd.ExtraFiles[0] != secretR || cmd.ExtraFiles[1] != readyW {
		t.Errorf("ExtraFiles = %v, want [secretR readyW]", cmd.ExtraFiles)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Errorf("SysProcAttr.Setsid not set: %+v", cmd.SysProcAttr)
	}
	if cmd.Stdin != devNull || cmd.Stdout != devNull || cmd.Stderr != devNull {
		t.Error("stdio not redirected to devNull")
	}

	cmdX11 := buildHolderCommand("/usr/bin/self", backendX11, secretR, readyW, devNull)
	if cmdX11.Args[2] != "x11" {
		t.Errorf("backendX11 arg = %q, want x11", cmdX11.Args[2])
	}
}

func TestWaitForReady_Success(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = w.Write([]byte{1})
		_ = w.Close()
	}()
	if err := waitForReady(r); err != nil {
		t.Fatalf("waitForReady() = %v, want nil", err)
	}
}

func TestWaitForReady_ClosedBeforeByte(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close() // simulates the holder exiting/failing before taking ownership
	if err := waitForReady(r); err == nil {
		t.Fatal("waitForReady() = nil, want an error")
	}
}

func TestWaitForReady_Timeout(t *testing.T) {
	orig := holderReadyTimeout
	holderReadyTimeout = 20 * time.Millisecond
	defer func() { holderReadyTimeout = orig }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }() // never write or close from "the child" - forces the timeout branch

	if err := waitForReady(r); err == nil {
		t.Fatal("waitForReady() = nil, want a timeout error")
	}
}
