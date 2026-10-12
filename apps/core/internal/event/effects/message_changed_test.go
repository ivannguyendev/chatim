package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMsgChangedDeclaresItsPolicy(t *testing.T) {
	e := newEditRig(t).changed.Effect()
	if e.Name != effects.MessageChangedName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.MessageChangedName, delay)
	}
}

func TestMsgChangedPublishesTheCurrentSnapshot(t *testing.T) {
	cases := []struct {
		name    string
		kind    domain.EditKind
		text    string
		subject string
	}{
		{"edit", domain.EditText, "v1", "evt.acme.message.4242.msg_edited"},
		{"delete", domain.EditDelete, "", "evt.acme.message.4242.msg_deleted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rg := newEditRig(t)
			rg.original(t, room, 1)
			f := rg.appendFact(t, room, 1, 1, c.kind, c.text)
			rg.project(t, f)
			if errs := rg.changed.Effect().Run(t.Context(), editRecs(room, 1, 1)); !allNil(errs, 1) {
				t.Fatalf("errs = %v", errs)
			}
			if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MessageChangeEventID(room, 0, 1, 1)}) {
				t.Fatalf("stored = %v", got)
			}
			if subj := rg.js.Stored()[0].Subject; subj != c.subject {
				t.Fatalf("subject = %q, want %q", subj, c.subject)
			}
			events, err := rg.js.Events()
			if want := pbconv.MessageChanged(domain.RoomGroup, rg.stored(t, 1), f); err != nil || !proto.Equal(events[0], want) {
				t.Fatalf("event = %v, %v; want the fast path event %v", events, err, want)
			}
			if rg.changed.Republished() != 1 || rg.changed.Dropped() != 0 {
				t.Fatalf("republished %d, dropped %d; want 1 and 0", rg.changed.Republished(), rg.changed.Dropped())
			}
		})
	}
}

func TestMsgChangedCountsOnlyEventsTheStreamLacked(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	f := rg.appendFact(t, room, 1, 1, domain.EditText, "v1")
	rg.project(t, f)
	fast, err := publish.Message("evt", room, pbconv.MessageEdited(domain.RoomGroup, rg.stored(t, 1), f))
	if err != nil {
		t.Fatalf("publish.Message: %v", err)
	}
	if _, err := rg.js.PublishMsgAsync(fast); err != nil {
		t.Fatalf("fast path publish: %v", err)
	}
	if errs := rg.changed.Effect().Run(t.Context(), editRecs(room, 1, 1)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.js.Stored()) != 1 || len(rg.js.Attempts()) != 2 || rg.changed.Republished() != 0 {
		t.Fatalf("stored %d, attempts %d, republished %d; want 1, 2 and 0", len(rg.js.Stored()), len(rg.js.Attempts()), rg.changed.Republished())
	}
}

func TestMsgChangedWaitsForTheProjection(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	rg.appendFact(t, room, 1, 1, domain.EditText, "v1")
	errs := rg.changed.Effect().Run(t.Context(), editRecs(room, 1, 1))
	if len(errs) != 1 || errs[0] == nil || len(rg.js.Attempts()) != 0 || rg.changed.Dropped() != 0 {
		t.Fatalf("errs = %v, attempts %d, dropped %d; want a retry and nothing sent", errs, len(rg.js.Attempts()), rg.changed.Dropped())
	}
}

func TestMsgChangedDropsWhatIsGone(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	rg.appendFact(t, room, 8, 1, domain.EditText, "no message")
	rg.original(t, 999, 1)
	rg.project(t, rg.appendFact(t, 999, 1, 1, domain.EditText, "no room"))
	recs := append(editRecs(room, 1, 5), editRecs(room, 8, 1)...)
	recs = append(recs, editRecs(999, 1, 1)...)
	if errs := rg.changed.Effect().Run(t.Context(), recs); !allNil(errs, 3) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.changed.Dropped() != 3 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 3 (missing fact, message, room)", len(rg.js.Attempts()), rg.changed.Dropped())
	}
}

func TestMsgChangedRetriesWhenTheStoreFails(t *testing.T) {
	eff, err := effects.NewMessageChanged(
		effects.MessageChangedDeps{Edits: brokenStore{}, Messages: brokenStore{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewMessageChanged: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), editRecs(room, 1, 1)); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}

func TestMessageChangedSkipsTheOriginalRow(t *testing.T) {
	js := &publishtest.JetStream{}
	eff, err := effects.NewMessageChanged(
		effects.MessageChangedDeps{Edits: brokenStore{}, Messages: brokenStore{}, Rooms: brokenStore{}, JS: js},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewMessageChanged: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), editRecs(room, 1, 0)); !allNil(errs, 1) || eff.Dropped() != 0 || len(js.Attempts()) != 0 {
		t.Fatalf("errs = %v, dropped %d, attempts %d; want nil, no store read, no drop and no event", errs, eff.Dropped(), len(js.Attempts()))
	}
}

func TestMsgChangedWaitsForThePubAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newEditRig(t)
		rg.original(t, room, 1)
		rg.project(t, rg.appendFact(t, room, 1, 1, domain.EditText, "v1"))
		rg.js.Hold()
		done := make(chan []error, 1)
		go func() { done <- rg.changed.Effect().Run(context.Background(), editRecs(room, 1, 1)) }()
		synctest.Wait()
		select {
		case errs := <-done:
			t.Fatalf("Run returned %v before the PubAck", errs)
		default:
		}
		rg.js.Release()
		if errs := <-done; !allNil(errs, 1) || rg.changed.Republished() != 1 {
			t.Fatalf("errs = %v, republished %d; want success after the PubAck", errs, rg.changed.Republished())
		}
	})
}

func TestNewMessageChangedRejectsBadInput(t *testing.T) {
	js, edits, msgs, mem := &publishtest.JetStream{}, memstore.NewEdits(), memstore.NewMessages(), memstore.NewRooms()
	cfg := effects.MessageChangedConfig{SubjectRoot: "evt"}
	for name, deps := range map[string]effects.MessageChangedDeps{
		"no edits":    {Messages: msgs, Rooms: mem, JS: js},
		"no messages": {Edits: edits, Rooms: mem, JS: js},
		"no rooms":    {Edits: edits, Messages: msgs, JS: js},
		"no js":       {Edits: edits, Messages: msgs, Rooms: mem},
	} {
		if _, err := effects.NewMessageChanged(deps, cfg); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewMessageChanged = %v, want ErrInvalidArgument", name, err)
		}
	}
	full := effects.MessageChangedDeps{Edits: edits, Messages: msgs, Rooms: mem, JS: js}
	if _, err := effects.NewMessageChanged(full, effects.MessageChangedConfig{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewMessageChanged without a subject root = %v, want ErrInvalidArgument", err)
	}
	eff, err := effects.NewMessageChanged(full, cfg)
	if err != nil {
		t.Fatalf("NewMessageChanged with defaults: %v", err)
	}
	if got := eff.Effect().Delay; got != effects.DefaultDelay {
		t.Fatalf("default delay = %v, want %v", got, effects.DefaultDelay)
	}
}
