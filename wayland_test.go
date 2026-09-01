//go:build linux

package clipboard

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestEncodeString(t *testing.T) {
	tests := []struct {
		s    string
		want []byte
	}{
		{"", []byte{1, 0, 0, 0, 0, 0, 0, 0}},          // len=1 (just NUL), padded to 4
		{"ab", []byte{3, 0, 0, 0, 'a', 'b', 0, 0}},    // len=3, padded to 4
		{"abc", []byte{4, 0, 0, 0, 'a', 'b', 'c', 0}}, // len=4, already aligned
	}
	for _, tt := range tests {
		got := encodeString(tt.s)
		if !bytes.Equal(got, tt.want) {
			t.Errorf("encodeString(%q) = % x, want % x", tt.s, got, tt.want)
		}
		if len(got)%4 != 0 {
			t.Errorf("encodeString(%q) length %d not 4-byte aligned", tt.s, len(got))
		}
	}
}

func TestEncodeBind(t *testing.T) {
	got := encodeBind(7, "wl_seat", 1, 42)
	var want []byte
	want = binary.LittleEndian.AppendUint32(want, 7)
	want = append(want, encodeString("wl_seat")...)
	want = binary.LittleEndian.AppendUint32(want, 1)
	want = binary.LittleEndian.AppendUint32(want, 42)
	if !bytes.Equal(got, want) {
		t.Errorf("encodeBind() = % x, want % x", got, want)
	}
}

func TestDecodeGlobal(t *testing.T) {
	body := append(encodeUint32(5), encodeString("zwlr_data_control_manager_v1")...)
	body = append(body, encodeUint32(2)...)

	name, iface, version, err := decodeGlobal(body)
	if err != nil {
		t.Fatalf("decodeGlobal() err = %v", err)
	}
	if name != 5 || iface != "zwlr_data_control_manager_v1" || version != 2 {
		t.Fatalf("decodeGlobal() = (%d, %q, %d), want (5, zwlr_data_control_manager_v1, 2)", name, iface, version)
	}
}

func TestDecodeGlobal_Malformed(t *testing.T) {
	if _, _, _, err := decodeGlobal([]byte{1, 2, 3}); err == nil {
		t.Fatal("decodeGlobal(short body) = nil error, want an error")
	}
}

func TestDecodeWaylandError(t *testing.T) {
	body := append(encodeUint32(9), encodeUint32(3)...)
	body = append(body, encodeString("no wlr-data-control")...)

	err := decodeWaylandError(body)
	if err == nil {
		t.Fatal("decodeWaylandError() = nil, want an error")
	}
	want := "clipboard: wayland protocol error on object 9 (code 3): no wlr-data-control"
	if err.Error() != want {
		t.Errorf("decodeWaylandError() = %q, want %q", err.Error(), want)
	}
}

// waylandSocketpair returns two connected *net.UnixConn endpoints backed
// by a real AF_UNIX SOCK_STREAM socketpair, so tests can exercise real
// SCM_RIGHTS fd passing without a real compositor.
func waylandSocketpair(t *testing.T) (client, server *net.UnixConn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	toConn := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "wayland-test-socket")
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatalf("net.FileConn: %v", err)
		}
		_ = f.Close() // FileConn dups the fd; the original is no longer needed
		uc, ok := c.(*net.UnixConn)
		if !ok {
			t.Fatalf("net.FileConn returned %T, want *net.UnixConn", c)
		}
		return uc
	}
	return toConn(fds[0]), toConn(fds[1])
}

// fakeCompositor is a minimal scripted server for waylandClient.run:
// it answers the registry roundtrip with wl_seat and
// zwlr_data_control_manager_v1 globals, ignores binds/offers/set_selection
// (their opcodes are fire-and-forget requests with no reply), confirms
// the second sync, then emits a send event (with a pipe fd) followed by a
// cancelled event.
type fakeCompositor struct {
	conn *waylandConn
}

func (f *fakeCompositor) readRequest() (waylandMessage, error) {
	return f.conn.readMessage()
}

func (f *fakeCompositor) sendEvent(objectID uint32, opcode uint16, args []byte) error {
	return f.conn.sendRequest(objectID, opcode, args)
}

func (f *fakeCompositor) sendEventWithFD(objectID uint32, opcode uint16, args []byte, fd int) error {
	size := 8 + len(args)
	msg := make([]byte, 0, size)
	msg = binary.LittleEndian.AppendUint32(msg, objectID)
	msg = binary.LittleEndian.AppendUint32(msg, uint32(size)<<16|uint32(opcode)) //nolint:gosec // G115: test-only fixed-shape message, never near uint32 range
	msg = append(msg, args...)
	oob := unix.UnixRights(fd)
	_, _, err := f.conn.nc.WriteMsgUnix(msg, oob, nil)
	return err
}

func TestWaylandClientRun(t *testing.T) {
	clientConn, serverConn := waylandSocketpair(t)
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	fc := &fakeCompositor{conn: &waylandConn{nc: serverConn}}

	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pipeR.Close() }()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- runFakeCompositor(fc, int(pipeW.Fd()))
	}()

	client := newWaylandClient(clientConn)
	var readyCalled bool
	err = client.run([]byte("hunter2"), func() { readyCalled = true })
	_ = pipeW.Close()

	if err != nil {
		t.Fatalf("run() = %v, want nil", err)
	}
	if !readyCalled {
		t.Error("ready callback was never called")
	}

	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("fake compositor: %v", serverErr)
	}

	got, err := io.ReadAll(pipeR)
	if err != nil {
		t.Fatalf("reading pipe: %v", err)
	}
	if string(got) != "hunter2" {
		t.Fatalf("data written to send fd = %q, want %q", got, "hunter2")
	}
}

// runFakeCompositor drives the server side of TestWaylandClientRun's
// socketpair, emulating just enough of a real compositor's responses.
func runFakeCompositor(fc *fakeCompositor, sendFD int) error {
	// get_registry
	if _, err := fc.readRequest(); err != nil {
		return err
	}
	// sync (registry roundtrip)
	syncReq, err := fc.readRequest()
	if err != nil {
		return err
	}
	syncID := binary.LittleEndian.Uint32(syncReq.body)

	const registryID = 2
	if err := fc.sendEvent(registryID, registryGlobalEvent, encodeGlobalForTest(1, "wl_seat", 1)); err != nil {
		return err
	}
	if err := fc.sendEvent(registryID, registryGlobalEvent, encodeGlobalForTest(2, "zwlr_data_control_manager_v1", 1)); err != nil {
		return err
	}
	if err := fc.sendEvent(syncID, callbackDoneEvent, nil); err != nil {
		return err
	}

	// bind(seat), bind(manager)
	if _, err := fc.readRequest(); err != nil {
		return err
	}
	if _, err := fc.readRequest(); err != nil {
		return err
	}
	// create_data_source
	sourceReq, err := fc.readRequest()
	if err != nil {
		return err
	}
	sourceID := binary.LittleEndian.Uint32(sourceReq.body)
	// offer x len(waylandMimeTypes)
	for range waylandMimeTypes {
		if _, err := fc.readRequest(); err != nil {
			return err
		}
	}
	// get_data_device
	if _, err := fc.readRequest(); err != nil {
		return err
	}
	// set_selection
	if _, err := fc.readRequest(); err != nil {
		return err
	}
	// confirm sync
	confirmReq, err := fc.readRequest()
	if err != nil {
		return err
	}
	confirmID := binary.LittleEndian.Uint32(confirmReq.body)
	if err := fc.sendEvent(confirmID, callbackDoneEvent, nil); err != nil {
		return err
	}

	// send event carrying the pipe fd, then cancelled.
	if err := fc.sendEventWithFD(sourceID, dataControlSourceSendEvent, encodeString("text/plain;charset=utf-8"), sendFD); err != nil {
		return err
	}
	// give the client a moment to have read and written the fd before we
	// signal cancellation, so the write above lands deterministically.
	time.Sleep(20 * time.Millisecond)
	return fc.sendEvent(sourceID, dataControlSourceCancelledEvent, nil)
}

func encodeGlobalForTest(name uint32, iface string, version uint32) []byte {
	buf := encodeUint32(name)
	buf = append(buf, encodeString(iface)...)
	buf = append(buf, encodeUint32(version)...)
	return buf
}

func TestWaylandClientRun_NoManagerGlobal(t *testing.T) {
	clientConn, serverConn := waylandSocketpair(t)
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	fc := &fakeCompositor{conn: &waylandConn{nc: serverConn}}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- func() error {
			if _, err := fc.readRequest(); err != nil { // get_registry
				return err
			}
			syncReq, err := fc.readRequest() // sync
			if err != nil {
				return err
			}
			syncID := binary.LittleEndian.Uint32(syncReq.body)
			// Only advertise wl_seat - no data-control manager, as on GNOME.
			if err := fc.sendEvent(2, registryGlobalEvent, encodeGlobalForTest(1, "wl_seat", 1)); err != nil {
				return err
			}
			return fc.sendEvent(syncID, callbackDoneEvent, nil)
		}()
	}()

	client := newWaylandClient(clientConn)
	err := client.run([]byte("secret"), func() { t.Error("ready should not be called") })
	if err == nil {
		t.Fatal("run() = nil, want an error when the compositor has no data-control manager")
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("fake compositor: %v", serverErr)
	}
}
