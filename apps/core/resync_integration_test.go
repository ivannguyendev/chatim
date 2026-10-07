package main

import (
	"bytes"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func TestRealInfraResyncDrillRepublishesWritesTheReaderMissed(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastEffects)
	env["RECONCILE_ENABLED"] = "false"
	core := startCore(t, it, env)
	began := time.Now().UTC()
	roomID := createRoom(t, dialCore(t, core.cfg))
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)

	st := mongostore.New(it.mongo.Database(core.cfg.MongoDB), mongostore.Options{})
	missed := make([]domain.Message, 3)
	want := make([]string, len(missed))
	for i := range missed {
		seq := uint64(i + 1)
		missed[i] = domain.Message{
			Room: room, Seq: seq, Tenant: itTenant, From: "migrator", Kind: domain.KindText, Text: "missed by the reader",
			CID: "missed-" + strconv.FormatUint(seq, 10), CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		}
		want[i] = pbconv.MessageEventID(room, 0, seq)
	}
	for i, res := range st.Insert(t.Context(), missed) {
		if res.Outcome != store.Inserted {
			t.Fatalf("insert missed[%d]: %+v", i, res)
		}
	}
	edit := domain.Edit{
		Room: room, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: itTenant, By: "migrator",
		Text: "edited while the reader was down", At: time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := st.Append(t.Context(), edit); err != nil {
		t.Fatalf("append an edit the reader missed: %v", err)
	}
	want = append(want, pbconv.MessageChangeEventID(room, 0, 1, 1))
	marked := time.Now().UTC().Truncate(time.Millisecond)
	reaction := domain.Reaction{Room: room, Seq: 2, Tenant: itTenant, User: "migrator", Emoji: "👍", At: marked}
	if _, _, err := itReactions(st).Set(t.Context(), reaction); err != nil {
		t.Fatalf("set a reaction the reader missed: %v", err)
	}
	pin := domain.PinAction{Room: room, PV: 1, Tenant: itTenant, Op: domain.PinOpPin, Seq: 3, By: "migrator", At: marked}
	if err := itPins(st).Append(t.Context(), pin); err != nil {
		t.Fatalf("append a pin the reader missed: %v", err)
	}
	want = append(want, pbconv.ReactionEventID(room, 0, 2, "migrator", 1), pbconv.ReactionCountsEventID(room, 0, 2, 1), pbconv.PinEventID(room, 1))
	assertNoLiveIDs(t, live, 3*time.Second, want...)

	opts := resync.Options{From: began.Add(-time.Minute), To: time.Now().UTC().Add(time.Minute), Tenant: itTenant, Rate: 100}
	var out bytes.Buffer
	ran := time.Now()
	if err := runResync(t.Context(), core.cfg, opts, quiet, &out); err != nil {
		t.Fatalf("runResync: %v (output %q)", err, out.String())
	}
	if got := strings.TrimSpace(out.String()); got != "resync rooms=1 room_records=1 message_records=3 edit_records=1 reaction_records=1 pin_records=1 dry_run=false" {
		t.Fatalf("resync output = %q, want one room, its room record, three message, one edit, one reaction and one pin record", got)
	}
	awaitLiveIDs(t, live, ran, want...)
	got, err := st.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: 1}})
	if err != nil || len(got) != 1 || got[0].Version != 1 || got[0].Text != edit.Text {
		t.Fatalf("seq 1 after resync = %+v, %v; want the missed edit projected at version 1", got, err)
	}
	counted, err := st.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: 2}})
	if err != nil || len(counted) != 1 || counted[0].Reactions.Version != 1 ||
		!slices.Equal(counted[0].Reactions.Counts, []domain.ReactionCount{{Emoji: "👍", Count: 1}}) {
		t.Fatalf("seq 2 after resync = %+v, %v; want the missed reaction counted at version 1", counted, err)
	}
	pins, err := st.PinState(t.Context(), room)
	if err != nil || pins.Version != 1 || len(pins.Pins) != 1 || pins.Pins[0].Seq != 3 || pins.Pins[0].PV != 1 {
		t.Fatalf("pins after resync = %+v, %v; want seq 3 pinned at version 1", pins, err)
	}
}
