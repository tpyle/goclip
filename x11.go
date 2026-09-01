//go:build linux

package clipboard

import (
	"errors"
	"fmt"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// x11Atoms holds the interned X11 atoms the CLIPBOARD selection owner
// needs to answer SelectionRequest events.
type x11Atoms struct {
	clipboard, targets, utf8String, str, text xproto.Atom
}

// x11Transport is the subset of X11 selection-owner operations
// runX11Loop needs, so its SelectionRequest/SelectionClear dispatch logic
// can be unit-tested against a fake without a real X server.
type x11Transport interface {
	waitForEvent() (xgb.Event, error)
	changeProperty(requestor xproto.Window, property, typ xproto.Atom, format byte, data []byte) error
	sendSelectionNotify(notify xproto.SelectionNotifyEvent) error
}

// x11Backend implements backend by taking ownership of the X11 CLIPBOARD
// selection (ICCCM) and serving it until superseded.
type x11Backend struct{}

func (x11Backend) serve(data []byte, ready func()) error {
	c, err := newX11Conn()
	if err != nil {
		return err
	}
	defer c.close()
	ready()
	return runX11Loop(c, c.atoms, data)
}

// realX11Conn owns a real X11 connection and the invisible window used to
// hold the CLIPBOARD selection - selections are owned by windows, not
// bare connections, so a real (if never-mapped) window is mandatory.
type realX11Conn struct {
	conn  *xgb.Conn
	win   xproto.Window
	atoms x11Atoms
}

// newX11Conn connects to the X server named by $DISPLAY, creates an
// invisible 1x1 window, and takes ownership of the CLIPBOARD selection on
// it. The connection is left open on return so the caller can serve
// SelectionRequest events; call close when done.
func newX11Conn() (*realX11Conn, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("clipboard: connect to X11: %w", err)
	}

	screen := xproto.Setup(conn).DefaultScreen(conn)
	win, err := xproto.NewWindowId(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("clipboard: allocate x11 window id: %w", err)
	}
	if err := xproto.CreateWindowChecked(conn, screen.RootDepth, win, screen.Root,
		0, 0, 1, 1, 0, xproto.WindowClassInputOutput, screen.RootVisual,
		xproto.CwEventMask, []uint32{xproto.EventMaskPropertyChange}).Check(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clipboard: create x11 window: %w", err)
	}

	atomVals, err := internX11Atoms(conn, "CLIPBOARD", "TARGETS", "UTF8_STRING", "STRING", "TEXT")
	if err != nil {
		xproto.DestroyWindow(conn, win)
		conn.Close()
		return nil, err
	}
	atoms := x11Atoms{
		clipboard:  atomVals[0],
		targets:    atomVals[1],
		utf8String: atomVals[2],
		str:        atomVals[3],
		text:       atomVals[4],
	}

	if err := xproto.SetSelectionOwnerChecked(conn, win, atoms.clipboard, xproto.TimeCurrentTime).Check(); err != nil {
		xproto.DestroyWindow(conn, win)
		conn.Close()
		return nil, fmt.Errorf("clipboard: set x11 selection owner: %w", err)
	}
	// SetSelectionOwner can silently lose a race with another client -
	// confirm we actually got it.
	ownerReply, err := xproto.GetSelectionOwner(conn, atoms.clipboard).Reply()
	if err != nil {
		xproto.DestroyWindow(conn, win)
		conn.Close()
		return nil, fmt.Errorf("clipboard: get x11 selection owner: %w", err)
	}
	if ownerReply.Owner != win {
		xproto.DestroyWindow(conn, win)
		conn.Close()
		return nil, errors.New("clipboard: another process took the X11 CLIPBOARD selection")
	}

	return &realX11Conn{conn: conn, win: win, atoms: atoms}, nil
}

func internX11Atoms(conn *xgb.Conn, names ...string) ([]xproto.Atom, error) {
	result := make([]xproto.Atom, len(names))
	for i, name := range names {
		reply, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply() //nolint:gosec // G115: atom names here are short fixed string literals, never near uint16 range
		if err != nil {
			return nil, fmt.Errorf("clipboard: intern x11 atom %s: %w", name, err)
		}
		result[i] = reply.Atom
	}
	return result, nil
}

func (c *realX11Conn) close() {
	xproto.DestroyWindow(c.conn, c.win)
	c.conn.Close()
}

func (c *realX11Conn) waitForEvent() (xgb.Event, error) {
	ev, xerr := c.conn.WaitForEvent()
	if xerr != nil {
		return nil, xerr
	}
	return ev, nil
}

func (c *realX11Conn) changeProperty(requestor xproto.Window, property, typ xproto.Atom, format byte, data []byte) error {
	//nolint:gosec // G115: data is either a small TARGETS atom list or clipboard text, never near uint32 range
	n := uint32(len(data))
	if format == 32 {
		n = uint32(len(data) / 4) //nolint:gosec // G115: see above
	}
	return xproto.ChangePropertyChecked(c.conn, xproto.PropModeReplace, requestor, property, typ, format, n, data).Check()
}

func (c *realX11Conn) sendSelectionNotify(notify xproto.SelectionNotifyEvent) error {
	return xproto.SendEventChecked(c.conn, false, notify.Requestor, xproto.EventMaskNoEvent, string(notify.Bytes())).Check()
}

// runX11Loop answers SelectionRequest events with data until ownership is
// lost (SelectionClear, meaning a new owner took over) or the transport
// errors.
func runX11Loop(t x11Transport, atoms x11Atoms, data []byte) error {
	for {
		ev, err := t.waitForEvent()
		if err != nil {
			return fmt.Errorf("clipboard: x11 protocol error: %w", err)
		}
		if ev == nil {
			return errors.New("clipboard: x11 connection closed")
		}
		switch e := ev.(type) {
		case xproto.SelectionClearEvent:
			return nil
		case xproto.SelectionRequestEvent:
			if err := respondToSelectionRequest(t, e, atoms, data); err != nil {
				return err
			}
		}
	}
}

// respondToSelectionRequest answers one SelectionRequest for TARGETS (the
// list of formats we support) or one of the text targets (the actual
// secret), or refuses unsupported targets per ICCCM (SelectionNotify with
// Property set to None).
func respondToSelectionRequest(t x11Transport, req xproto.SelectionRequestEvent, atoms x11Atoms, data []byte) error {
	property := req.Property
	if property == xproto.AtomNone {
		property = req.Target // pre-ICCCM clients expect the target used as the property name
	}

	ok := false
	var err error
	switch req.Target {
	case atoms.targets:
		supported := []xproto.Atom{atoms.targets, atoms.utf8String, atoms.str, atoms.text}
		buf := make([]byte, 4*len(supported))
		for i, a := range supported {
			xgb.Put32(buf[i*4:], uint32(a))
		}
		err = t.changeProperty(req.Requestor, property, xproto.AtomAtom, 32, buf)
		ok = err == nil
	case atoms.utf8String, atoms.str, atoms.text:
		err = t.changeProperty(req.Requestor, property, req.Target, 8, data)
		ok = err == nil
	}
	if err != nil {
		return err
	}

	notify := xproto.SelectionNotifyEvent{
		Time:      req.Time,
		Requestor: req.Requestor,
		Selection: req.Selection,
		Target:    req.Target,
		Property:  xproto.AtomNone,
	}
	if ok {
		notify.Property = property
	}
	return t.sendSelectionNotify(notify)
}
