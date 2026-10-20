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
	if CreateKey("acme", "alice", "r1") == CreateKey("other", "alice", "r1") {
		t.Fatal("create keys of two tenants are equal")
	}
}

func TestCreateKeysSpreadByTenantAndUserNotBySlotZero(t *testing.T) {
	if CreateKey("acme", "alice", "r1").shardSeed() != CreateKey("acme", "alice", "r2").shardSeed() {
		t.Fatal("two requests of one caller land on different shards")
	}
	seeds := map[uint64]bool{}
	for _, u := range []string{"alice", "bob", "carol", "dave", "erin"} {
		seeds[CreateKey("acme", u, "r1").shardSeed()%16] = true
	}
	if len(seeds) < 2 {
		t.Fatalf("five callers share one of 16 shards: %v", seeds)
	}
	if k := RequestKey(42, "alice", "r1"); k.shardSeed() != uint64(slotmap.Of(42)) {
		t.Fatalf("room key seed = %d, want slot %d", k.shardSeed(), slotmap.Of(42))
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
