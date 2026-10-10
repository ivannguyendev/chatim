package grpcsrv_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type countingPages struct {
	grpcsrv.PageReader
	mu    sync.Mutex
	finds [][]uint64
}

func (c *countingPages) Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error) {
	seqs := make([]uint64, len(keys))
	for i, k := range keys {
		seqs[i] = k.Seq
	}
	c.mu.Lock()
	c.finds = append(c.finds, seqs)
	c.mu.Unlock()
	return c.PageReader.Find(ctx, room, keys)
}

func (c *countingPages) take() [][]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.finds
	c.finds = nil
	return out
}

func (o options) pageReader(p grpcsrv.PageReader) grpcsrv.PageReader {
	if o.pages == nil {
		return p
	}
	return o.pages(p)
}

func previewsSeen(t *testing.T, rg *rig, ctx context.Context, room string, after uint64) []*chatimv1.ReplyPreview {
	t.Helper()
	req := &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_AFTER, Seq: after}
	if after == 0 {
		req.Anchor = chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST
	}
	resp, err := rg.client.GetHistory(ctx, req)
	if err != nil {
		t.Fatalf("GetHistory after %d: %v", after, err)
	}
	out := make([]*chatimv1.ReplyPreview, len(resp.GetMessages()))
	for i, m := range resp.GetMessages() {
		out[i] = m.GetReplyPreview()
	}
	return out
}

func TestHistoryQuotesEachParentAsTheReaderSeesIt(t *testing.T) {
	clock := &testClock{now: sentBase}
	pages := &countingPages{}
	rg := newRig(t, options{now: clock.Now, pages: func(p grpcsrv.PageReader) grpcsrv.PageReader {
		pages.PageReader = p
		return pages
	}})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for i, text := range []string{"one", "two", "three", "four"} {
		rg.insertAt(t, room, uint64(i+1), text, sentAtSeq(uint64(i+1)))
	}
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 3}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	for i, parent := range []uint64{1, 2, 3, 4, 5, 1} {
		rg.insertReply(t, room, uint64(i+5), parent)
	}
	for _, step := range []func() error{
		func() error {
			_, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 4})
			return err
		},
		func() error {
			_, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 10})
			return err
		},
		func() error {
			clock.set(sentAtSeq(1).Add(500 * time.Millisecond))
			_, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room})
			return err
		},
	} {
		if err := step(); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	pages.take()
	if got := previewsSeen(t, rg, bob, room, 0)[:4]; slices.ContainsFunc(got, func(p *chatimv1.ReplyPreview) bool { return p != nil }) {
		t.Fatalf("messages that quote nothing got previews %v", got)
	}
	if finds := pages.take(); len(finds) != 0 {
		t.Fatalf("a page whose quotes are all in the page read %v, want no extra read", finds)
	}
	quote := func(seq uint64, sender, text string, deleted, hidden bool) *chatimv1.ReplyPreview {
		return &chatimv1.ReplyPreview{Seq: seq, Sender: sender, Text: text, Deleted: deleted, Hidden: hidden}
	}
	cases := []struct {
		name string
		ctx  context.Context
		want []*chatimv1.ReplyPreview
	}{
		{"bob", bob, []*chatimv1.ReplyPreview{
			quote(1, "alice", "", false, true), quote(2, "alice", "two", false, false), quote(3, "alice", "", true, false),
			quote(4, "alice", "", false, true), quote(5, "alice", "r5", false, false), nil,
		}},
		{"alice", alice, []*chatimv1.ReplyPreview{
			quote(1, "alice", "one", false, false), quote(2, "alice", "two", false, false), quote(3, "alice", "", true, false),
			quote(4, "alice", "four", false, false), quote(5, "alice", "r5", false, false), quote(1, "alice", "one", false, false),
		}},
	}
	for _, c := range cases {
		got := previewsSeen(t, rg, c.ctx, room, 4)
		if !slices.EqualFunc(got, c.want, func(a, b *chatimv1.ReplyPreview) bool { return proto.Equal(a, b) }) {
			t.Fatalf("%s previews = %v, want %v", c.name, got, c.want)
		}
		if finds := pages.take(); len(finds) != 1 || !slices.Equal(finds[0], []uint64{1, 2, 3, 4}) {
			t.Fatalf("%s reads = %v, want one read of the parents outside the page", c.name, finds)
		}
	}
}
