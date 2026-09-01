//go:build linux

package clipboard

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// waylandDisplayID is the pre-existing wl_display singleton's object ID -
// every Wayland connection starts with this object already bound.
const waylandDisplayID uint32 = 1

// waylandMimeTypes are the clipboard mime types offered to pasting
// clients, in the same spirit as wl-copy's own default offer list.
var waylandMimeTypes = []string{
	"text/plain;charset=utf-8",
	"text/plain",
	"UTF8_STRING",
	"STRING",
	"TEXT",
}

// Wayland request/event opcodes this client needs. Only wl_display,
// wl_registry, wl_callback, and the wlr-data-control-unstable-v1
// protocol are implemented - see the README for why no general-purpose
// Wayland client library is used.
const (
	displayGetRegistryOpcode uint16 = 1
	displaySyncOpcode        uint16 = 0
	displayErrorEvent        uint16 = 0

	registryBindOpcode  uint16 = 0
	registryGlobalEvent uint16 = 0

	callbackDoneEvent uint16 = 0

	dataControlManagerCreateSourceOpcode  uint16 = 0
	dataControlManagerGetDataDeviceOpcode uint16 = 1

	dataControlSourceOfferOpcode    uint16 = 0
	dataControlSourceSendEvent      uint16 = 0
	dataControlSourceCancelledEvent uint16 = 1

	dataControlDeviceSetSelectionOpcode uint16 = 0
)

// waylandBackend implements backend by hand-rolling the small subset of
// the core Wayland wire protocol plus the wlr-data-control-unstable-v1
// protocol extension needed to set the clipboard without holding
// keyboard focus. That extension isn't part of core Wayland - it's
// exposed by wlroots-based compositors (sway, Hyprland) and newer KDE
// Plasma/KWin; GNOME/Mutter implements neither it nor its ext-data-control
// successor, so Wayland copy fails there with a clear error, exactly like
// wl-copy itself already does today - see the README's Limitations.
type waylandBackend struct{}

func (waylandBackend) serve(data []byte, ready func()) error {
	nc, err := dialWayland()
	if err != nil {
		return err
	}
	defer nc.Close() //nolint:errcheck // best-effort cleanup; nothing actionable on close failure

	c := newWaylandClient(nc)
	return c.run(data, ready)
}

// dialWayland connects to the compositor's Unix socket, following the
// same resolution libwayland-client uses: $WAYLAND_DISPLAY (defaulting to
// "wayland-0"), relative to $XDG_RUNTIME_DIR unless already absolute.
func dialWayland() (*net.UnixConn, error) {
	display := os.Getenv("WAYLAND_DISPLAY")
	if display == "" {
		display = "wayland-0"
	}
	path := display
	if !filepath.IsAbs(path) {
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if runtimeDir == "" {
			return nil, errors.New("clipboard: $XDG_RUNTIME_DIR is not set")
		}
		path = filepath.Join(runtimeDir, display)
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("clipboard: connect to wayland socket: %w", err)
	}
	return conn, nil
}

// waylandMessage is one decoded Wayland wire message: sender object,
// opcode, and its still wire-encoded argument bytes.
type waylandMessage struct {
	sender uint32
	opcode uint16
	body   []byte
}

// waylandConn buffers reads off a Wayland Unix socket and separates the
// regular byte stream from file descriptors received via SCM_RIGHTS
// ancillary data. Fds are queued in the order they arrive, independent of
// exactly which underlying recvmsg() call happened to carry them - the
// kernel guarantees that ordering, not any particular chunking.
type waylandConn struct {
	nc      *net.UnixConn
	pending []byte
	fds     []int
}

func (c *waylandConn) fill() error {
	buf := make([]byte, 4096)
	oob := make([]byte, unix.CmsgSpace(16*4)) // room for several fds in one recvmsg
	n, oobn, _, _, err := c.nc.ReadMsgUnix(buf, oob)
	if err != nil {
		return fmt.Errorf("clipboard: read wayland socket: %w", err)
	}
	if n == 0 && oobn == 0 {
		return errors.New("clipboard: wayland connection closed")
	}
	c.pending = append(c.pending, buf[:n]...)
	if oobn > 0 {
		cmsgs, err := unix.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			return fmt.Errorf("clipboard: parse wayland control message: %w", err)
		}
		for _, cmsg := range cmsgs {
			fds, err := unix.ParseUnixRights(&cmsg)
			if err != nil {
				continue // not a rights control message - nothing to collect
			}
			c.fds = append(c.fds, fds...)
		}
	}
	return nil
}

// readExact blocks until at least n bytes are buffered, then consumes and
// returns exactly n of them.
func (c *waylandConn) readExact(n int) ([]byte, error) {
	for len(c.pending) < n {
		if err := c.fill(); err != nil {
			return nil, err
		}
	}
	out := c.pending[:n]
	c.pending = c.pending[n:]
	return out, nil
}

// popFD blocks until a file descriptor has arrived and returns it.
func (c *waylandConn) popFD() (int, error) {
	for len(c.fds) == 0 {
		if err := c.fill(); err != nil {
			return -1, err
		}
	}
	fd := c.fds[0]
	c.fds = c.fds[1:]
	return fd, nil
}

func (c *waylandConn) readMessage() (waylandMessage, error) {
	header, err := c.readExact(8)
	if err != nil {
		return waylandMessage{}, err
	}
	sender := binary.LittleEndian.Uint32(header[0:4])
	sizeAndOpcode := binary.LittleEndian.Uint32(header[4:8])
	size := sizeAndOpcode >> 16
	opcode := uint16(sizeAndOpcode & 0xffff)
	if size < 8 {
		return waylandMessage{}, fmt.Errorf("clipboard: invalid wayland message size %d", size)
	}
	body, err := c.readExact(int(size) - 8)
	if err != nil {
		return waylandMessage{}, err
	}
	return waylandMessage{sender: sender, opcode: opcode, body: body}, nil
}

func (c *waylandConn) sendRequest(objectID uint32, opcode uint16, args []byte) error {
	size := 8 + len(args)
	msg := make([]byte, 0, size)
	msg = binary.LittleEndian.AppendUint32(msg, objectID)
	msg = binary.LittleEndian.AppendUint32(msg, uint32(size)<<16|uint32(opcode)) //nolint:gosec // G115: all requests this client sends are tiny fixed-shape protocol messages, never near uint32 range
	msg = append(msg, args...)
	if _, err := c.nc.Write(msg); err != nil {
		return fmt.Errorf("clipboard: send wayland request: %w", err)
	}
	return nil
}

// waylandClient drives one clipboard-copy session over an already
// connected Wayland socket: discover the seat and the
// zwlr_data_control_manager_v1 global, create and offer a data source,
// set it as the selection, then serve send/cancelled events.
type waylandClient struct {
	conn   *waylandConn
	nextID uint32
}

func newWaylandClient(nc *net.UnixConn) *waylandClient {
	return &waylandClient{conn: &waylandConn{nc: nc}, nextID: 2} // 1 is wl_display
}

func (c *waylandClient) allocID() uint32 {
	id := c.nextID
	c.nextID++
	return id
}

func (c *waylandClient) run(data []byte, ready func()) error {
	registryID := c.allocID()
	if err := c.conn.sendRequest(waylandDisplayID, displayGetRegistryOpcode, encodeUint32(registryID)); err != nil {
		return err
	}
	syncID := c.allocID()
	if err := c.conn.sendRequest(waylandDisplayID, displaySyncOpcode, encodeUint32(syncID)); err != nil {
		return err
	}

	var seatName, seatVersion, managerName, managerVersion uint32
	var haveSeat, haveManager bool

registryLoop:
	for {
		msg, err := c.conn.readMessage()
		if err != nil {
			return fmt.Errorf("clipboard: wayland registry roundtrip: %w", err)
		}
		switch {
		case msg.sender == waylandDisplayID && msg.opcode == displayErrorEvent:
			return decodeWaylandError(msg.body)
		case msg.sender == registryID && msg.opcode == registryGlobalEvent:
			name, iface, version, err := decodeGlobal(msg.body)
			if err != nil {
				return err
			}
			switch iface {
			case "wl_seat":
				if !haveSeat {
					seatName, seatVersion, haveSeat = name, version, true
				}
			case "zwlr_data_control_manager_v1":
				if !haveManager {
					managerName, managerVersion, haveManager = name, version, true
				}
			}
		case msg.sender == syncID && msg.opcode == callbackDoneEvent:
			break registryLoop
		}
	}

	if !haveManager {
		return errors.New("clipboard: compositor does not support wlr-data-control-unstable-v1 - Wayland clipboard access is unavailable in this session")
	}
	if !haveSeat {
		return errors.New("clipboard: compositor advertised no wl_seat")
	}
	// Only the bare object is needed (no capabilities/name events are
	// consumed), so binding version 1 is always safe.
	seatVersion, managerVersion = min(seatVersion, 1), min(managerVersion, 1)

	seatID := c.allocID()
	if err := c.conn.sendRequest(registryID, registryBindOpcode, encodeBind(seatName, "wl_seat", seatVersion, seatID)); err != nil {
		return err
	}
	managerID := c.allocID()
	if err := c.conn.sendRequest(registryID, registryBindOpcode, encodeBind(managerName, "zwlr_data_control_manager_v1", managerVersion, managerID)); err != nil {
		return err
	}

	sourceID := c.allocID()
	if err := c.conn.sendRequest(managerID, dataControlManagerCreateSourceOpcode, encodeUint32(sourceID)); err != nil {
		return err
	}
	for _, mime := range waylandMimeTypes {
		if err := c.conn.sendRequest(sourceID, dataControlSourceOfferOpcode, encodeString(mime)); err != nil {
			return err
		}
	}

	deviceID := c.allocID()
	if err := c.conn.sendRequest(managerID, dataControlManagerGetDataDeviceOpcode, encodeUint32Pair(deviceID, seatID)); err != nil {
		return err
	}
	if err := c.conn.sendRequest(deviceID, dataControlDeviceSetSelectionOpcode, encodeUint32(sourceID)); err != nil {
		return err
	}

	// A second roundtrip confirms the compositor processed set_selection
	// without an immediate protocol error before reporting ready.
	confirmID := c.allocID()
	if err := c.conn.sendRequest(waylandDisplayID, displaySyncOpcode, encodeUint32(confirmID)); err != nil {
		return err
	}
confirmLoop:
	for {
		msg, err := c.conn.readMessage()
		if err != nil {
			return fmt.Errorf("clipboard: wayland set_selection confirmation: %w", err)
		}
		switch {
		case msg.sender == waylandDisplayID && msg.opcode == displayErrorEvent:
			return decodeWaylandError(msg.body)
		case msg.sender == confirmID && msg.opcode == callbackDoneEvent:
			break confirmLoop
		}
	}

	ready()

	for {
		msg, err := c.conn.readMessage()
		if err != nil {
			return fmt.Errorf("clipboard: wayland protocol error: %w", err)
		}
		switch {
		case msg.sender == waylandDisplayID && msg.opcode == displayErrorEvent:
			return decodeWaylandError(msg.body)
		case msg.sender == sourceID && msg.opcode == dataControlSourceSendEvent:
			fd, err := c.conn.popFD()
			if err != nil {
				return fmt.Errorf("clipboard: receive wayland send fd: %w", err)
			}
			writeAndCloseFD(fd, data)
		case msg.sender == sourceID && msg.opcode == dataControlSourceCancelledEvent:
			return nil
		}
	}
}

// writeAndCloseFD writes data to a pipe fd handed to us by a
// zwlr_data_control_source_v1.send event and closes it. Best-effort: if
// the requesting client stops reading, the write may fail or block only
// as long as the pipe buffer allows - acceptable for secrets, which are
// always far smaller than a pipe buffer.
func writeAndCloseFD(fd int, data []byte) {
	f := os.NewFile(uintptr(fd), "wayland-send")
	_, _ = f.Write(data)
	_ = f.Close()
}

func encodeUint32(v uint32) []byte {
	return binary.LittleEndian.AppendUint32(nil, v)
}

func encodeUint32Pair(a, b uint32) []byte {
	buf := binary.LittleEndian.AppendUint32(nil, a)
	return binary.LittleEndian.AppendUint32(buf, b)
}

// encodeString encodes a Wayland wire string: a length-prefixed,
// NUL-terminated byte string padded to a 4-byte boundary. The length
// includes the NUL terminator.
func encodeString(s string) []byte {
	n := len(s) + 1
	padded := (n + 3) &^ 3
	buf := make([]byte, 4+padded)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(n)) //nolint:gosec // G115: mime type / interface name strings here are short fixed literals
	copy(buf[4:], s)
	return buf
}

// encodeBind encodes wl_registry.bind's "generic new_id" wire form:
// (uint name, string interface, uint version, uint new_id). The
// interface/version fields only appear here because bind's target
// interface isn't statically known from the protocol definition - every
// other new_id argument this client sends is a plain uint32.
func encodeBind(name uint32, iface string, version, newID uint32) []byte {
	buf := encodeUint32(name)
	buf = append(buf, encodeString(iface)...)
	buf = append(buf, encodeUint32(version)...)
	buf = append(buf, encodeUint32(newID)...)
	return buf
}

// decodeGlobal decodes a wl_registry::global event body: (uint name,
// string interface, uint version).
func decodeGlobal(body []byte) (name uint32, iface string, version uint32, err error) {
	if len(body) < 8 {
		return 0, "", 0, errors.New("clipboard: malformed wayland global event")
	}
	name = binary.LittleEndian.Uint32(body[0:4])
	strLen := binary.LittleEndian.Uint32(body[4:8])
	if strLen == 0 || 8+int(strLen) > len(body) {
		return 0, "", 0, errors.New("clipboard: malformed wayland global event interface string")
	}
	iface = string(body[8 : 8+strLen-1]) // drop the NUL terminator
	padded := (int(strLen) + 3) &^ 3
	versionOffset := 8 + padded
	if versionOffset+4 > len(body) {
		return 0, "", 0, errors.New("clipboard: malformed wayland global event version")
	}
	version = binary.LittleEndian.Uint32(body[versionOffset : versionOffset+4])
	return name, iface, version, nil
}

// decodeWaylandError decodes a wl_display::error event body: (object_id
// uint, code uint, message string).
func decodeWaylandError(body []byte) error {
	if len(body) < 8 {
		return errors.New("clipboard: malformed wayland error event")
	}
	objectID := binary.LittleEndian.Uint32(body[0:4])
	code := binary.LittleEndian.Uint32(body[4:8])
	msg := ""
	if len(body) >= 12 {
		strLen := binary.LittleEndian.Uint32(body[8:12])
		if strLen > 0 && 12+int(strLen) <= len(body) {
			msg = string(body[12 : 12+strLen-1])
		}
	}
	return fmt.Errorf("clipboard: wayland protocol error on object %d (code %d): %s", objectID, code, msg)
}
