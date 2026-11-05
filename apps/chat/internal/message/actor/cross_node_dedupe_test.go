package actor_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/chat/internal/message/actor"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/platform/testlog"
)

func TestCommittedCIDIsAckedByAnotherChatNodeWithoutInsert(t *testing.T) {
	w := newWorld(t)
	spyB := &spyMessages{Messages: w.msgs}
	regA := signalCommits(w.registry(t, "chat-a", quiet, 0))
	a := startChatNode(t, w.msgs, w.rooms, regA)
	b := startChatNode(t, spyB, w.rooms, w.registry(t, "chat-b", quiet, 0))
	mustSend(t, b, cmd(roomA, "bob", "warm"))
	before := spyB.insertedCount()

	ack := mustSend(t, a, cmd(roomA, "alice", "x"))
	awaitSignal(t, "chat A cid commit", regA.committed)
	if got := mustSend(t, b, cmd(roomA, "alice", "x")); !sameAck(got, ack) {
		t.Fatalf("chat B answered %+v, want chat A's %+v", got, ack)
	}
	if n := spyB.insertedCount(); n != before {
		t.Fatalf("chat B inserted %d messages for a cid committed by chat A", n-before)
	}
	assertStoredIn(t, w, "x", ack)
}

func TestSameCIDRacingOnTwoChatNodesIsStoredOnce(t *testing.T) {
	w := newWorld(t)
	nodes := []*actor.Router{
		startChatNode(t, w.msgs, w.rooms, w.registry(t, "chat-a", quiet, 0)),
		startChatNode(t, w.msgs, w.rooms, w.registry(t, "chat-b", quiet, 0)),
	}
	for i, chat := range nodes {
		mustSend(t, chat, cmd(roomA, "bob", fmt.Sprintf("warm-%d", i)))
	}
	for i := range 20 {
		c := cmd(roomA, "alice", fmt.Sprintf("race-%d", i))
		results := make(chan sendResult, len(nodes))
		for _, chat := range nodes {
			go func() {
				ack, _, err := sendRetrying(chat, c)
				results <- sendResult{ack: ack, err: err}
			}()
		}
		x, y := <-results, <-results
		if x.err != nil || y.err != nil {
			t.Fatalf("%s: errors %v, %v", c.CID, x.err, y.err)
		}
		if !sameAck(x.ack, y.ack) {
			t.Fatalf("%s acked differently by the two chat nodes: %+v and %+v", c.CID, x.ack, y.ack)
		}
		docs := storedCIDs(t, w.msgs, roomA)[c.CID]
		if len(docs) != 1 {
			t.Fatalf("%s stored %d times, want once", c.CID, len(docs))
		}
		assertAckMatches(t, x.ack, docs[0])
	}
}

func TestCrashedChatNodeBlocksTheCIDUntilItsReservationExpires(t *testing.T) {
	w := newWorld(t)
	sink := newBlackhole()
	a, crash := runRouter(t, w.msgs, w.rooms, sink, w.registry(t, "chat-a", quiet, 0), clusterConfig)
	lost := sendAsync(context.Background(), a, cmd(roomA, "alice", "x"))
	awaitSignal(t, "chat A submit after its reservation", sink.submitted)
	crash()
	expectErr(t, (<-lost).err, domain.ErrRetryLater)
	if v := w.cidValue("x"); v != "p:chat-a" {
		t.Fatalf("crash changed the reservation to %q", v)
	}

	regB := signalCommits(w.registry(t, "chat-b", quiet, 0))
	b := startChatNode(t, w.msgs, w.rooms, regB)
	_, err := b.Send(context.Background(), cmd(roomA, "alice", "x"))
	expectErr(t, err, domain.ErrRetryLater)
	w.mr.FastForward(dedupe.DefaultPendingTTL)
	ack := mustSend(t, b, cmd(roomA, "alice", "x"))
	awaitSignal(t, "chat B cid commit", regB.committed)
	assertStoredIn(t, w, "x", ack)
	if v := w.cidValue("x"); !strings.HasPrefix(v, "c:") {
		t.Fatalf("redis holds %q after chat B committed, want a committed record", v)
	}
}

func TestFailedWriteReleasesOrKeepsTheCIDForOtherChatNodes(t *testing.T) {
	tests := map[string]struct {
		write outcome
		held  bool
	}{
		"rejected write is released": {rejected, false},
		"unconfirmed write is kept":  {lostUnknown, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			sub := &fakeSubmitter{store: w.msgs}
			sub.alwaysDo(tt.write)
			a, _ := runRouter(t, w.msgs, w.rooms, sub, w.registry(t, "chat-a", quiet, 0), clusterConfig)
			if _, err := a.Send(context.Background(), cmd(roomA, "alice", "x")); err == nil {
				t.Fatal("failing write was acked")
			}
			b := startChatNode(t, w.msgs, w.rooms, w.registry(t, "chat-b", quiet, 0))
			ack, err := b.Send(context.Background(), cmd(roomA, "alice", "x"))
			if tt.held {
				expectErr(t, err, domain.ErrRetryLater)
				if v := w.cidValue("x"); v != "p:chat-a" {
					t.Fatalf("reservation = %q, want chat A's kept", v)
				}
				return
			}
			if err != nil {
				t.Fatalf("retry on chat B after a released cid: %v", err)
			}
			assertStoredIn(t, w, "x", ack)
		})
	}
}

func TestRedisOutageFallsBackToTheLocalCacheAndRecovers(t *testing.T) {
	w := newWorld(t)
	sink := &testlog.Sink{}
	reg := signalCommits(w.registry(t, "chat-a", sink.Logger(), 20*time.Millisecond))
	chat := startChatNode(t, w.msgs, w.rooms, reg)
	mustSend(t, chat, cmd(roomA, "alice", "before"))
	awaitSignal(t, "cid commit before the outage", reg.committed)

	w.mr.Close()
	first := mustSend(t, chat, cmd(roomA, "alice", "x"))
	for i := range 5 {
		time.Sleep(30 * time.Millisecond)
		mustSend(t, chat, cmd(roomA, "alice", fmt.Sprintf("down-%d", i)))
	}
	if again := mustSend(t, chat, cmd(roomA, "alice", "x")); !sameAck(again, first) {
		t.Fatalf("resend during the outage got %+v, want %+v", again, first)
	}
	assertStoredIn(t, w, "x", first)
	if n := sink.Count(degradedMsg); n != 1 {
		t.Fatalf("logged degraded %d times during one outage, want 1", n)
	}

	if err := w.mr.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for i := 0; ; i++ {
		cid := fmt.Sprintf("up-%d", i)
		mustSend(t, chat, cmd(roomA, "alice", cid))
		if strings.HasPrefix(w.cidValue(cid), "c:") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("redis never used again within 30s after it came back")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if d, r := sink.Count(degradedMsg), sink.Count(recoveredMsg); d != 1 || r != 1 {
		t.Fatalf("logged degraded %d and recovered %d times, want 1 each", d, r)
	}
}

func assertStoredIn(t *testing.T, w *world, cid string, ack actor.Ack) {
	t.Helper()
	docs := storedCIDs(t, w.msgs, roomA)[cid]
	if len(docs) != 1 {
		t.Fatalf("stored %d copies of %s, want 1", len(docs), cid)
	}
	assertAckMatches(t, ack, docs[0])
}
