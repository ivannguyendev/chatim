package dedupe

import (
	"testing"
	"time"
)

func TestRequestsOnRedisKeepTheirOwnKeys(t *testing.T) {
	mr, rdb := newRedis(t)
	s := newStore(t, rdb, "core-a", nil)
	r := newRequests(t, s)
	cid, req := key("r1"), requestKey("r1")

	expectStatuses(t, reserve(t, s, cid), Reserved)
	expectBegin(t, r, req, RequestNew)
	expectValue(t, mr, req, "p:core-a")
	if req.String() != "chatim:req:42:alice:r1" || cid.String() != "chatim:cid:42:alice:r1" {
		t.Fatalf("keys = %s and %s", req, cid)
	}

	r.Finish(t.Context(), req, sampleRecord)
	expectValue(t, mr, req, committedValue(sampleRecord))
	expectValue(t, mr, cid, "p:core-a")
	if ttl := mr.TTL(req.String()); ttl != DefaultCommittedTTL {
		t.Fatalf("committed request ttl = %v, want %v", ttl, DefaultCommittedTTL)
	}
}

func TestARestartedCoreSeesTheCommittedRequest(t *testing.T) {
	_, rdb := newRedis(t)
	reg := signalingStore{Store: newStore(t, rdb, "core-a", nil), committed: make(chan struct{}, 1)}
	b, _ := startBatcher(t, reg, BatchConfig{})
	before := newRequests(t, b)
	k := requestKey("r1")

	expectBegin(t, before, k, RequestNew)
	before.Finish(t.Context(), k, Record{Seq: 3, CreatedAt: time.Now()})
	select {
	case <-reg.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("finished request never reached redis")
	}

	after := newRequests(t, newStore(t, rdb, "core-a", nil))
	expectBegin(t, after, k, RequestDone)
}

func TestTwoCoresSeeABusyRequest(t *testing.T) {
	_, rdb := newRedis(t)
	a := newRequests(t, newStore(t, rdb, "core-a", nil))
	b := newRequests(t, newStore(t, rdb, "core-b", nil))
	k := requestKey("r1")

	expectBegin(t, a, k, RequestNew)
	expectBegin(t, b, k, RequestBusy)
	expectBegin(t, a, k, RequestBusy)

	a.Cancel(t.Context(), k)
	expectBegin(t, b, k, RequestNew)
	b.Finish(t.Context(), k, Record{Seq: 1, CreatedAt: time.Now()})
	expectBegin(t, a, k, RequestDone)
}

func TestRequestsFallBackToMemoryWhileRedisIsDown(t *testing.T) {
	mr, rdb := newRedis(t)
	r := newRequests(t, newStore(t, rdb, "core-a", nil))
	done, open := requestKey("done"), requestKey("open")
	mr.Close()

	expectBegin(t, r, done, RequestNew)
	r.Finish(t.Context(), done, Record{Seq: 2, CreatedAt: time.Now()})
	expectBegin(t, r, done, RequestDone)
	expectBegin(t, r, open, RequestNew)
	r.Cancel(t.Context(), open)
	expectBegin(t, r, open, RequestNew)
}
