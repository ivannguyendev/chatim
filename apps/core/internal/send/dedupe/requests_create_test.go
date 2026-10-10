package dedupe

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestCreateKeyNamesTenantUserAndRequestOnSlotZero(t *testing.T) {
	k := CreateKey("acme", "alice", "r1")
	if got := k.String(); got != "chatim:req:create:acme:alice:r1" {
		t.Fatalf("create key = %q", got)
	}
	if k.Room != 0 || slotmap.Of(k.Room) != 0 {
		t.Fatalf("create key room %d on slot %d, want room 0 on slot 0", k.Room, slotmap.Of(k.Room))
	}
	if CreateKey("acme", "alice", "r1") == CreateKey("other", "alice", "r1") {
		t.Fatal("create keys of two tenants are equal")
	}
}

func TestBeginReturnsTheFinishedRecord(t *testing.T) {
	_, rdb := newRedis(t)
	r := newRequests(t, newStore(t, rdb, "core-a", nil))
	k := CreateKey("acme", "alice", "r1")
	rec := Record{Seq: 8812, CreatedAt: time.UnixMilli(1_700_000_000_123).UTC()}

	expectBegin(t, r, k, RequestNew)
	r.Finish(t.Context(), k, rec)
	for name, req := range map[string]*Requests{"same core": r, "restarted core": newRequests(t, newStore(t, rdb, "core-a", nil))} {
		status, got, err := req.Begin(t.Context(), k)
		if err != nil || status != RequestDone || got.Seq != rec.Seq || !got.CreatedAt.Equal(rec.CreatedAt) {
			t.Fatalf("%s: Begin = %v, %+v, %v; want done with %+v", name, status, got, err, rec)
		}
	}
}
