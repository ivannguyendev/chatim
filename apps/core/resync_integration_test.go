package main

import (
	"bytes"
	"maps"
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
		Text: "edited while the reader was down", Prev: "missed by the reader", At: time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := st.Append(t.Context(), edit); err != nil {
		t.Fatalf("append an edit the reader missed: %v", err)
	}
	want = append(want, pbconv.MessageChangeEventID(room, 0, 1, 1))
	assertNoLiveIDs(t, live, 3*time.Second, want...)

	opts := resync.Options{From: began.Add(-time.Minute), To: time.Now().UTC().Add(time.Minute), Tenant: itTenant, Rate: 100}
	var out bytes.Buffer
	ran := time.Now()
	if err := runResync(t.Context(), core.cfg, opts, quiet, &out); err != nil {
		t.Fatalf("runResync: %v (output %q)", err, out.String())
	}
	if got := strings.TrimSpace(out.String()); got != "resync rooms=1 room_records=1 message_records=3 edit_records=1 dry_run=false" {
		t.Fatalf("resync output = %q, want one room, its room record, three message records and one edit record", got)
	}
	awaitLiveIDs(t, live, ran, want...)
	got, err := st.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: 1}})
	if err != nil || len(got) != 1 || got[0].Version != 1 || got[0].Text != edit.Text {
		t.Fatalf("seq 1 after resync = %+v, %v; want the missed edit projected at version 1", got, err)
	}
}
