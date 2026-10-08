package grpcsrv_test

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type shown struct {
	seq             uint64
	text            string
	deleted, hidden bool
	version         uint32
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

var sentBase = time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)

func (rg *rig) insertAt(t *testing.T, room string, seq uint64, text string, at time.Time) {
	t.Helper()
	m := domain.Message{
		Room: roomNumber(t, room), Seq: seq, Tenant: "acme", From: "alice", Kind: domain.KindText,
		Text: text, CID: "c-" + strconv.FormatUint(seq, 10), CreatedAt: at,
	}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert seq %d: %+v", seq, res)
	}
}

func historySeen(t *testing.T, rg *rig, ctx context.Context, room string) []shown {
	t.Helper()
	resp, err := rg.client.GetHistory(ctx, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST})
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	var out []shown
	for _, m := range resp.GetMessages() {
		out = append(out, shown{m.GetSeq(), m.GetText(), m.GetDeleted(), m.GetHidden(), m.GetVer()})
	}
	return out
}

func TestHistoryShowsPlaceholdersPerViewer(t *testing.T) {
	clock := &testClock{now: sentBase}
	rg := newRig(t, options{now: clock.Now})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for i, text := range []string{"one", "two", "three", "four"} {
		rg.insertAt(t, room, uint64(i+1), text, sentBase.Add(time.Duration(i)*time.Second))
	}
	steps := []func() error{
		func() error {
			_, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 2})
			return err
		},
		func() error {
			_, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 4, Text: "four!"})
			return err
		},
		func() error {
			_, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 3})
			return err
		},
		func() error {
			_, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room})
			return err
		},
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	cases := []struct {
		name string
		ctx  context.Context
		want []shown
	}{
		{"bob", bob, []shown{{1, "", false, true, 0}, {2, "", true, false, 1}, {3, "", false, true, 0}, {4, "four!", false, false, 1}}},
		{"alice", alice, []shown{{1, "one", false, false, 0}, {2, "", true, false, 1}, {3, "three", false, false, 0}, {4, "four!", false, false, 1}}},
	}
	for _, c := range cases {
		if got := historySeen(t, rg, c.ctx, room); !slices.Equal(got, c.want) {
			t.Fatalf("%s history = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestHideAndClearHistoryThroughTheService(t *testing.T) {
	clock := &testClock{now: sentBase}
	rg := newRig(t, options{now: clock.Now})
	room := rg.createGroup(t, "acme", "alice", "bob")
	bob := as(t, "acme", "bob")
	rg.insertAt(t, room, 1, "a", sentBase)
	rg.insertAt(t, room, 2, "b", sentBase.Add(time.Second))
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	if seqs, err := rg.hidden.HiddenIn(t.Context(), "bob", roomNumber(t, room), 0, 1, 2); err != nil || !slices.Equal(seqs, []uint64{2}) {
		t.Fatalf("bob hidden = %v, %v; want [2]", seqs, err)
	}
	mark := sentBase.Add(time.Second + 1500*time.Microsecond)
	clock.set(mark)
	cleared, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room})
	if want := mark.Truncate(time.Millisecond); err != nil || !cleared.GetClearedAt().AsTime().Equal(want) {
		t.Fatalf("ClearHistory = %v, %v; want cleared at %v", cleared, err, want)
	}
	clock.set(sentBase)
	again, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room})
	if want := mark.Truncate(time.Millisecond); err != nil || !again.GetClearedAt().AsTime().Equal(want) {
		t.Fatalf("ClearHistory with an earlier clock = %v, %v; want the mark kept at %v", again, err, want)
	}
	rg.insertAt(t, room, 3, "c", mark.Add(time.Millisecond))
	want := []shown{{1, "", false, true, 0}, {2, "", false, true, 0}, {3, "c", false, false, 0}}
	if got := historySeen(t, rg, bob, room); !slices.Equal(got, want) {
		t.Fatalf("bob history = %+v, want %+v", got, want)
	}
}
