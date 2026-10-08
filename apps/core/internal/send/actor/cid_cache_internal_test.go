package actor

import (
	"errors"
	"testing"
	"time"
)

func TestCIDCacheBoundsCommittedAcksButNeverPendingOnes(t *testing.T) {
	d := newCIDCache(2, time.Minute)
	pending := dedupeKey{user: "alice", cid: "p"}
	owner := newRequest(SendCmd{})
	if d.join(pending, owner) {
		t.Fatal("first sight of a cid was treated as a duplicate")
	}
	for _, cid := range []string{"c1", "c2", "c3"} {
		k := dedupeKey{user: "alice", cid: cid}
		d.join(k, newRequest(SendCmd{}))
		d.commit(k, Ack{Seq: 1})
	}
	if n := d.acks.Len(); n != 2 {
		t.Fatalf("committed acks = %d, want bounded to 2", n)
	}
	waiter := newRequest(SendCmd{})
	if !d.join(pending, waiter) {
		t.Fatal("duplicate of a pending cid was not attached")
	}
	boom := errors.New("boom")
	d.fail(pending, boom)
	for _, q := range []*request{owner, waiter} {
		if got := <-q.reply; !errors.Is(got.err, boom) {
			t.Fatalf("pending caller got %v, want %v", got.err, boom)
		}
	}
	if d.join(pending, newRequest(SendCmd{})) {
		t.Fatal("failed cid was remembered")
	}
}
