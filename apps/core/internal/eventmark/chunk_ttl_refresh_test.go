package eventmark_test

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	chunk0 = "chatim:evtack:4242:0:0"
	chunk1 = "chatim:evtack:4242:0:1"
	chunk3 = "chatim:evtack:4242:3:0"
)

type clockedStore struct {
	*eventmark.Store
	mr *miniredis.Miniredis
	at time.Time
}

func newClockedStore(t *testing.T) *clockedStore {
	t.Helper()
	mr, s := newStore(t)
	cs := &clockedStore{Store: s, mr: mr, at: time.Unix(1_700_000_000, 0)}
	eventmark.SetClock(s, func() time.Time { return cs.at })
	return cs
}

func (cs *clockedStore) advance(d time.Duration) {
	cs.at = cs.at.Add(d)
	cs.mr.FastForward(d)
}

func (cs *clockedStore) mark(t *testing.T, keys ...store.MsgKey) {
	t.Helper()
	if err := cs.Mark(t.Context(), keys); err != nil {
		t.Fatalf("Mark(%v): %v", keys, err)
	}
}

func (cs *clockedStore) wantTTL(t *testing.T, chunk string, want time.Duration) {
	t.Helper()
	if got := cs.mr.TTL(chunk); got != want {
		t.Fatalf("TTL(%s) = %v, want %v", chunk, got, want)
	}
}

func TestChunkTTLIsRefreshedAtMostEveryQuarterTTL(t *testing.T) {
	cs := newClockedStore(t)
	cs.mark(t, key(0, 1))
	cs.wantTTL(t, chunk0, time.Hour)
	cs.advance(10 * time.Minute)
	before := cs.mr.CommandCount()
	cs.mark(t, key(0, 2))
	if sent := cs.mr.CommandCount() - before; sent != 1 {
		t.Fatalf("second Mark in the same chunk sent %d commands, want 1 SETBIT", sent)
	}
	cs.wantTTL(t, chunk0, 50*time.Minute)
	if got, err := cs.Acked(t.Context(), []store.MsgKey{key(0, 2)}); err != nil || !got[0] {
		t.Fatalf("Acked(seq 2) = %v, %v; want [true], nil", got, err)
	}
	cs.advance(6 * time.Minute)
	cs.mark(t, key(0, 3))
	cs.wantTTL(t, chunk0, time.Hour)
}

func TestEveryNewChunkGetsItsTTL(t *testing.T) {
	cs := newClockedStore(t)
	cs.mark(t, key(0, 1))
	cs.advance(10 * time.Minute)
	cs.mark(t, key(0, 2), key(0, 8192), key(3, 1))
	cs.wantTTL(t, chunk0, 50*time.Minute)
	cs.wantTTL(t, chunk1, time.Hour)
	cs.wantTTL(t, chunk3, time.Hour)
}

func TestFailedMarkDoesNotCountAsRefresh(t *testing.T) {
	cs := newClockedStore(t)
	cs.mr.SetError("LOADING redis is loading")
	if err := cs.Mark(t.Context(), []store.MsgKey{key(0, 1)}); err == nil {
		t.Fatal("Mark with redis failing = nil error")
	}
	cs.mr.SetError("")
	cs.advance(2 * time.Second)
	cs.mark(t, key(0, 1))
	cs.wantTTL(t, chunk0, time.Hour)
}
