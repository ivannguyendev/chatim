package itest

import (
	"maps"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func readFlagged(t *testing.T, st *mongostore.Store, room uint64, user string) bool {
	t.Helper()
	docs, _, err := st.EventStates().Pending(t.Context(), store.StateMembers, time.Unix(0, 0), time.Now().Add(time.Hour), store.PendingCursor{}, store.MaxPendingPage)
	if err != nil {
		t.Fatalf("Pending(members): %v", err)
	}
	for _, d := range docs {
		if d.Room == room && d.User == user && d.State&store.BitReadPosition != 0 {
			return true
		}
	}
	return false
}

func TestRealInfraReadsOfOneWindowShareOneEventAndClearEveryFlag(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastSweep)
	env["READ_EVENT_WINDOW"], env["SWEEP_MIN_AGE"] = "3s", "6s"
	core := startCore(t, it, env)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	st := itStore(it, core)
	seq := sendAs(t, client, itUser, roomID, "read-window-1", "read me")
	start := time.Now()
	var entries []*chatimv1.ReadEntry
	for _, user := range []string{itUser, "bob"} {
		resp, err := client.MarkRead(callerAs(t.Context(), user), &chatimv1.MarkReadRequest{RoomId: roomID, Seq: seq})
		if err != nil || resp.GetReadSeq() != seq {
			t.Fatalf("MarkRead as %s = %v, %v; want read up to %d", user, resp, err, seq)
		}
		entries = append(entries, &chatimv1.ReadEntry{User: user, ReadSeq: seq, ReadVer: resp.GetReadVer()})
	}
	ev := awaitLiveEvent(t, live, pbconv.ReadPositionsEventID(room, entries))
	if got := ev.GetReadPositions().GetEntries(); len(got) != 2 || ev.GetActor() != "" {
		t.Fatalf("read_positions = %v, want both readers in one event with no actor", ev)
	}
	deadline := start.Add(core.cfg.Sweep.MinAge)
	for _, user := range []string{itUser, "bob"} {
		for readFlagged(t, st, room, user) {
			if time.Now().After(deadline) {
				t.Fatalf("read flag of %s still set %v after MarkRead, want it cleared after the PubAck before the sweeper could act", user, time.Since(start))
			}
			time.Sleep(itActivityPoll)
		}
	}
}
