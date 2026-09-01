//go:build linux

package clipboard

import (
	"errors"
	"testing"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type changePropertyCall struct {
	requestor    xproto.Window
	property, ty xproto.Atom
	format       byte
	data         []byte
}

type fakeX11Transport struct {
	events   []xgb.Event
	eventErr error // returned once, after all events are exhausted

	changeCalls []changePropertyCall
	changeErr   error
	notifyCalls []xproto.SelectionNotifyEvent
	notifyErr   error
}

func (f *fakeX11Transport) waitForEvent() (xgb.Event, error) {
	if len(f.events) > 0 {
		e := f.events[0]
		f.events = f.events[1:]
		return e, nil
	}
	if f.eventErr != nil {
		return nil, f.eventErr
	}
	return nil, errors.New("fakeX11Transport: no more scripted events")
}

func (f *fakeX11Transport) changeProperty(requestor xproto.Window, property, typ xproto.Atom, format byte, data []byte) error {
	f.changeCalls = append(f.changeCalls, changePropertyCall{requestor, property, typ, format, append([]byte(nil), data...)})
	return f.changeErr
}

func (f *fakeX11Transport) sendSelectionNotify(notify xproto.SelectionNotifyEvent) error {
	f.notifyCalls = append(f.notifyCalls, notify)
	return f.notifyErr
}

func testAtoms() x11Atoms {
	return x11Atoms{
		clipboard:  1,
		targets:    2,
		utf8String: 3,
		str:        4,
		text:       5,
	}
}

func TestRunX11Loop_SelectionClearEndsCleanly(t *testing.T) {
	f := &fakeX11Transport{events: []xgb.Event{xproto.SelectionClearEvent{}}}
	if err := runX11Loop(f, testAtoms(), []byte("secret")); err != nil {
		t.Fatalf("runX11Loop() = %v, want nil", err)
	}
	if len(f.changeCalls) != 0 || len(f.notifyCalls) != 0 {
		t.Fatalf("expected no property/notify calls, got %d/%d", len(f.changeCalls), len(f.notifyCalls))
	}
}

func TestRunX11Loop_TargetsRequest(t *testing.T) {
	atoms := testAtoms()
	req := xproto.SelectionRequestEvent{
		Time: 1, Requestor: 42, Selection: atoms.clipboard, Target: atoms.targets, Property: 99,
	}
	f := &fakeX11Transport{events: []xgb.Event{req, xproto.SelectionClearEvent{}}}

	if err := runX11Loop(f, atoms, []byte("secret")); err != nil {
		t.Fatalf("runX11Loop() = %v, want nil", err)
	}

	if len(f.changeCalls) != 1 {
		t.Fatalf("changeProperty calls = %d, want 1", len(f.changeCalls))
	}
	call := f.changeCalls[0]
	if call.requestor != 42 || call.property != 99 || call.ty != xproto.AtomAtom || call.format != 32 {
		t.Fatalf("unexpected changeProperty call: %+v", call)
	}
	wantAtoms := []xproto.Atom{atoms.targets, atoms.utf8String, atoms.str, atoms.text}
	if len(call.data) != 4*len(wantAtoms) {
		t.Fatalf("TARGETS payload length = %d, want %d", len(call.data), 4*len(wantAtoms))
	}
	for i, a := range wantAtoms {
		got := xgb.Get32(call.data[i*4:])
		if got != uint32(a) {
			t.Errorf("TARGETS atom[%d] = %d, want %d", i, got, a)
		}
	}

	if len(f.notifyCalls) != 1 {
		t.Fatalf("sendSelectionNotify calls = %d, want 1", len(f.notifyCalls))
	}
	notify := f.notifyCalls[0]
	if notify.Requestor != 42 || notify.Property != 99 || notify.Target != atoms.targets {
		t.Fatalf("unexpected SelectionNotify: %+v", notify)
	}
}

func TestRunX11Loop_TextRequest(t *testing.T) {
	atoms := testAtoms()
	req := xproto.SelectionRequestEvent{
		Time: 1, Requestor: 7, Selection: atoms.clipboard, Target: atoms.utf8String, Property: xproto.AtomNone,
	}
	f := &fakeX11Transport{events: []xgb.Event{req, xproto.SelectionClearEvent{}}}

	if err := runX11Loop(f, atoms, []byte("hunter2")); err != nil {
		t.Fatalf("runX11Loop() = %v, want nil", err)
	}

	if len(f.changeCalls) != 1 {
		t.Fatalf("changeProperty calls = %d, want 1", len(f.changeCalls))
	}
	call := f.changeCalls[0]
	// Property was None on the request, so ICCCM says fall back to using
	// the target atom itself as the property name.
	if call.property != atoms.utf8String || call.ty != atoms.utf8String || call.format != 8 {
		t.Fatalf("unexpected changeProperty call: %+v", call)
	}
	if string(call.data) != "hunter2" {
		t.Fatalf("changeProperty data = %q, want %q", call.data, "hunter2")
	}
	if len(f.notifyCalls) != 1 || f.notifyCalls[0].Property != atoms.utf8String {
		t.Fatalf("unexpected notify: %+v", f.notifyCalls)
	}
}

func TestRunX11Loop_UnsupportedTargetRefused(t *testing.T) {
	atoms := testAtoms()
	const unsupported xproto.Atom = 12345
	req := xproto.SelectionRequestEvent{
		Time: 1, Requestor: 7, Selection: atoms.clipboard, Target: unsupported, Property: 50,
	}
	f := &fakeX11Transport{events: []xgb.Event{req, xproto.SelectionClearEvent{}}}

	if err := runX11Loop(f, atoms, []byte("secret")); err != nil {
		t.Fatalf("runX11Loop() = %v, want nil", err)
	}
	if len(f.changeCalls) != 0 {
		t.Fatalf("expected no changeProperty call for an unsupported target, got %d", len(f.changeCalls))
	}
	if len(f.notifyCalls) != 1 || f.notifyCalls[0].Property != xproto.AtomNone {
		t.Fatalf("expected a refusal notify (Property=None), got %+v", f.notifyCalls)
	}
}

func TestRunX11Loop_ChangePropertyErrorPropagates(t *testing.T) {
	atoms := testAtoms()
	req := xproto.SelectionRequestEvent{Target: atoms.utf8String, Requestor: 1, Property: 1}
	wantErr := errors.New("boom")
	f := &fakeX11Transport{events: []xgb.Event{req}, changeErr: wantErr}

	err := runX11Loop(f, atoms, []byte("x"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("runX11Loop() = %v, want %v", err, wantErr)
	}
	if len(f.notifyCalls) != 0 {
		t.Fatalf("expected no notify after a changeProperty failure, got %d", len(f.notifyCalls))
	}
}

func TestRunX11Loop_WaitForEventErrorPropagates(t *testing.T) {
	wantErr := errors.New("connection reset")
	f := &fakeX11Transport{eventErr: wantErr}

	err := runX11Loop(f, testAtoms(), []byte("x"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("runX11Loop() = %v, want wrapping %v", err, wantErr)
	}
}
