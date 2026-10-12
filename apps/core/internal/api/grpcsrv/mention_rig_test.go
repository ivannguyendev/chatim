package grpcsrv_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	toBob     = domain.MentionTarget{Kind: domain.MentionUser, ID: "bob"}
	toOps     = domain.MentionTarget{Kind: domain.MentionGroup, ID: "ops"}
	toHanoi   = domain.MentionTarget{Kind: domain.MentionGroup, ID: "hanoi"}
	toAll     = domain.MentionTarget{Kind: domain.MentionAll}
	mentionT0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

func (o options) mentionLister(m grpcsrv.MentionLister) grpcsrv.MentionLister {
	if o.listing == nil {
		return m
	}
	return o.listing(m)
}

type mentionRig struct {
	*rig
	clock      *testClock
	alice, bob context.Context
	seqs       map[uint64]uint64
}

func newMentionRig(t *testing.T, o options) *mentionRig {
	t.Helper()
	clock := &testClock{now: mentionT0}
	o.now = clock.Now
	return &mentionRig{rig: newRig(t, o), clock: clock, alice: as(t, "acme", "alice"), bob: as(t, "acme", "bob"), seqs: map[uint64]uint64{}}
}

func sec(n int) time.Time { return mentionT0.Add(time.Duration(n) * time.Second) }

func (rg *mentionRig) room(t *testing.T, at int, members ...string) uint64 {
	t.Helper()
	rg.clock.set(sec(at))
	id, err := ids.ParseRoomID(rg.createGroup(t, "acme", "alice", members...))
	if err != nil {
		t.Fatalf("ParseRoomID: %v", err)
	}
	return id
}

func (rg *mentionRig) join(t *testing.T, room uint64, at int, user string) {
	t.Helper()
	rg.clock.set(sec(at))
	req := &chatimv1.AddMembersRequest{RoomId: ids.FormatRoomID(room), Users: []string{user}, RequestId: rand.Text()}
	if _, err := rg.client.AddMembers(rg.alice, req); err != nil {
		t.Fatalf("AddMembers(%s): %v", user, err)
	}
}

func (rg *mentionRig) clear(t *testing.T, room uint64, at int) {
	t.Helper()
	rg.clock.set(sec(at))
	if _, err := rg.client.ClearHistory(rg.bob, &chatimv1.ClearHistoryRequest{RoomId: ids.FormatRoomID(room)}); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
}

func (rg *mentionRig) read(t *testing.T, room, seq uint64, worker bool) {
	t.Helper()
	if _, err := rg.client.MarkRead(rg.bob, &chatimv1.MarkReadRequest{RoomId: ids.FormatRoomID(room), Seq: seq}); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if !worker {
		return
	}
	if err := rg.mentions.MarkRead(t.Context(), room, "bob", seq); err != nil {
		t.Fatalf("mentions MarkRead: %v", err)
	}
}

func (rg *mentionRig) post(t *testing.T, room uint64, text string, at int, targets ...domain.MentionTarget) uint64 {
	t.Helper()
	rg.seqs[room]++
	seq := rg.seqs[room]
	m := domain.Message{Room: room, Seq: seq, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: text, CID: fmt.Sprintf("c-%d-%d", room, seq), CreatedAt: sec(at)}
	if r := rg.msgs.Insert(t.Context(), []domain.Message{m}); r[0].Outcome != store.Inserted {
		t.Fatalf("Insert(%s) = %+v", text, r[0])
	}
	set := store.MentionSet{Key: store.KeyOf(m), Tenant: "acme", Sender: "alice", Targets: targets, CreatedAt: m.CreatedAt, At: m.CreatedAt}
	if err := rg.mentions.ApplyMentions(t.Context(), set); err != nil {
		t.Fatalf("ApplyMentions(%s): %v", text, err)
	}
	for _, tg := range targets {
		if tg.Kind != domain.MentionUser {
			if err := rg.rooms.RaiseSharedMention(t.Context(), room, m.CreatedAt); err != nil {
				t.Fatalf("RaiseSharedMention: %v", err)
			}
		}
	}
	return seq
}

func (rg *mentionRig) list(t *testing.T, req *chatimv1.ListMentionsRequest) (texts []string, read []bool, next string) {
	t.Helper()
	resp, err := rg.client.ListMentions(rg.bob, req)
	if err != nil {
		t.Fatalf("ListMentions(%v): %v", req, err)
	}
	for _, it := range resp.GetItems() {
		texts = append(texts, it.GetMessage().GetText())
		read = append(read, it.GetRead())
	}
	return texts, read, resp.GetNext()
}

type countingLister struct {
	grpcsrv.MentionLister
	mu     sync.Mutex
	shared [][]uint64
}

func (c *countingLister) ListShared(ctx context.Context, q store.SharedMentionQuery) ([]domain.Mention, error) {
	rooms := make([]uint64, len(q.Rooms))
	for i, r := range q.Rooms {
		rooms[i] = r.Room
	}
	c.mu.Lock()
	c.shared = append(c.shared, rooms)
	c.mu.Unlock()
	return c.MentionLister.ListShared(ctx, q)
}

func (c *countingLister) calls() [][]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.shared
	c.shared = nil
	return out
}
