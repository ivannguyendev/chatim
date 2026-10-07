package mongostore

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func samplePinAction() domain.PinAction {
	return domain.PinAction{Room: 7_340_000_001, PV: 3, Tenant: "acme", Op: domain.PinOpUnpin, Thread: 2, Seq: 42, By: "bob", At: codecTime}
}

func TestPinActionCodecRoundTrip(t *testing.T) {
	a := samplePinAction()
	doc, err := encodePinAction(a)
	if err != nil {
		t.Fatalf("encodePinAction: %v", err)
	}
	if !bytes.Equal(doc.ID, keys.Pin(a.Room, a.PV)) || doc.Op != 2 {
		t.Fatalf("_id = %x, op = %d; want keys.Pin and 2", doc.ID, doc.Op)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "action", "thread_root", "seq", "created_by", "created_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodePinAction(back)
	if err != nil || !got.At.Equal(a.At) {
		t.Fatalf("decodePinAction = %+v, %v; want %+v", got, err, a)
	}
	got.At = a.At
	if got != a {
		t.Fatalf("decoded %+v, want %+v", got, a)
	}
}

func TestEncodePinActionRejectsInvalid(t *testing.T) {
	for name, mutate := range map[string]func(*domain.PinAction){
		"zero version":            func(a *domain.PinAction) { a.PV = 0 },
		"version above max int64": func(a *domain.PinAction) { a.PV = math.MaxInt64 + 1 },
		"room above max int64":    func(a *domain.PinAction) { a.Room = math.MaxInt64 + 1 },
		"thread above max int64":  func(a *domain.PinAction) { a.Thread = math.MaxInt64 + 1 },
		"seq above max int64":     func(a *domain.PinAction) { a.Seq = math.MaxInt64 + 1 },
		"unknown op":              func(a *domain.PinAction) { a.Op = 3 },
	} {
		a := samplePinAction()
		mutate(&a)
		if _, err := encodePinAction(a); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: encodePinAction = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestDecodePinActionRejectsCorruptDocs(t *testing.T) {
	for name, mutate := range map[string]func(*pinActionDoc){
		"short id":        func(d *pinActionDoc) { d.ID = d.ID[:8] },
		"unknown op":      func(d *pinActionDoc) { d.Op = 7 },
		"negative thread": func(d *pinActionDoc) { d.Thread = -1 },
		"negative seq":    func(d *pinActionDoc) { d.Seq = -1 },
	} {
		d, err := encodePinAction(samplePinAction())
		if err != nil {
			t.Fatalf("encodePinAction: %v", err)
		}
		mutate(&d)
		if _, err := decodePinAction(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodePinAction = %v, want errCorrupt", name, err)
		}
	}
}

func TestPinStateCodecRoundTrip(t *testing.T) {
	s := domain.PinState{Pins: []domain.Pin{{Seq: 9, By: "bob", At: codecTime, PV: 4}, {Thread: 2, Seq: 7, By: "alice", At: codecTime, PV: 1}}, Version: 5}
	pins, pv, err := encodePinState(s)
	if err != nil || pv != 5 {
		t.Fatalf("encodePinState = %d, %v; want pv 5", pv, err)
	}
	back, raw := roundTrip(t, pinStateDoc{Pins: pins, PV: pv})
	if got, want := fieldNames(t, raw), []string{"pins", "pin_ver"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if got, want := fieldNames(t, raw.Lookup("pins", "1").Document()), []string{"thread_root", "seq", "pinned_by", "pinned_at", "pin_ver"}; !slices.Equal(got, want) {
		t.Fatalf("pin fields = %v, want %v", got, want)
	}
	got, err := decodePinState(back)
	if err != nil || got.Version != 5 || len(got.Pins) != 2 || got.Pins[0].Seq != 9 || got.Pins[1].Thread != 2 || !got.Pins[1].At.Equal(codecTime) {
		t.Fatalf("decodePinState = %+v, %v; want %+v", got, err, s)
	}
	if got, err := decodePinState(pinStateDoc{}); err != nil || got.Version != 0 || got.Pins != nil {
		t.Fatalf("decodePinState(empty) = %+v, %v; want the zero state", got, err)
	}
	for name, d := range map[string]pinStateDoc{
		"negative version": {PV: -1},
		"negative seq":     {Pins: []pinDoc{{Seq: -1, PV: 1}}, PV: 1},
		"negative pv":      {Pins: []pinDoc{{Seq: 1, PV: -1}}, PV: 1},
	} {
		if _, err := decodePinState(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodePinState = %v, want errCorrupt", name, err)
		}
	}
}
