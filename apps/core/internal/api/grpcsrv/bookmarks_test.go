package grpcsrv_test

import (
	"context"
	"encoding/base64"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func steppingClock() func() time.Time {
	var mu sync.Mutex
	at := time.Now().UTC().Truncate(time.Millisecond)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		at = at.Add(time.Second)
		return at
	}
}

type bookmarkRig struct {
	*rig
	alice, bob context.Context
	a, b       string
}

func newBookmarkRig(t *testing.T) *bookmarkRig {
	t.Helper()
	rg := &bookmarkRig{rig: newRig(t, options{now: steppingClock()}), alice: as(t, "acme", "alice"), bob: as(t, "acme", "bob")}
	rg.a = rg.createGroup(t, "acme", "alice", "bob")
	rg.b = rg.createGroup(t, "acme", "bob", "alice")
	for _, text := range []string{"one", "two", "three", "four"} {
		rg.send(t, rg.alice, rg.a, "c-"+text, text)
	}
	rg.send(t, rg.alice, rg.b, "c-bee", "bee")
	return rg
}

func (rg *bookmarkRig) mark(t *testing.T, ctx context.Context, room string, seq uint64, on, wantChanged bool) {
	t.Helper()
	resp, err := rg.client.SetBookmark(ctx, &chatimv1.SetBookmarkRequest{RoomId: room, Seq: seq, On: on})
	if err != nil || resp.GetChanged() != wantChanged {
		t.Fatalf("SetBookmark(%s/%d, %v) = %v, %v; want changed %v", room, seq, on, resp, err, wantChanged)
	}
}

func (rg *bookmarkRig) list(t *testing.T, before string, limit uint32) *chatimv1.ListBookmarksResponse {
	t.Helper()
	resp, err := rg.client.ListBookmarks(rg.alice, &chatimv1.ListBookmarksRequest{Before: before, Limit: limit})
	if err != nil {
		t.Fatalf("ListBookmarks(%q, %d): %v", before, limit, err)
	}
	return resp
}

func describe(items []*chatimv1.BookmarkItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		m := it.GetMessage()
		out[i] = m.GetRoomId() + "/" + m.GetText() + "/" + m.GetSender()
		if !it.GetAvailable() {
			out[i] = m.GetRoomId() + "/" + strconv.FormatUint(m.GetSeq(), 10) + "/gone"
		}
		if !it.GetAvailable() && (m.GetText() != "" || m.GetSender() != "" || m.GetDeleted() || m.GetHidden()) {
			out[i] += "/leaked"
		}
	}
	return out
}

func TestListBookmarksKeepsWhatTheReaderCanNoLongerSee(t *testing.T) {
	rg := newBookmarkRig(t)
	for _, seq := range []uint64{1, 2, 3} {
		rg.mark(t, rg.alice, rg.a, seq, true, true)
	}
	rg.mark(t, rg.alice, rg.b, 1, true, true)
	rg.mark(t, rg.alice, rg.a, 4, true, true)
	rg.mark(t, rg.alice, rg.a, 1, true, false)
	if _, err := rg.client.DeleteMessage(rg.alice, &chatimv1.DeleteMessageRequest{RoomId: rg.a, Seq: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if _, err := rg.client.HideMessage(rg.alice, &chatimv1.HideMessageRequest{RoomId: rg.a, Seq: 3}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	if _, err := rg.client.RemoveMember(rg.bob, &chatimv1.RemoveMemberRequest{RoomId: rg.b, User: "alice"}); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	want := []string{rg.a + "/four/alice", rg.b + "/1/gone", rg.a + "/3/gone", rg.a + "/2/gone", rg.a + "/one/alice"}
	var got []string
	before := ""
	for range 3 {
		page := rg.list(t, before, 2)
		got = append(got, describe(page.GetItems())...)
		before = page.GetNext()
	}
	if !slices.Equal(got, want) || before != "" {
		t.Fatalf("pages = %v, end cursor %q; want %v and no cursor after the short page", got, before, want)
	}
	if _, err := rg.client.ClearHistory(rg.alice, &chatimv1.ClearHistoryRequest{RoomId: rg.a}); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	cleared := []string{rg.a + "/4/gone", rg.b + "/1/gone", rg.a + "/3/gone", rg.a + "/2/gone", rg.a + "/1/gone"}
	if all := rg.list(t, "", 0); !slices.Equal(describe(all.GetItems()), cleared) || all.GetNext() != "" {
		t.Fatalf("after clear = %v next %q, want %v", describe(all.GetItems()), all.GetNext(), cleared)
	}
	rg.mark(t, rg.alice, rg.a, 4, false, true)
	if rest := rg.list(t, "", 0); len(rest.GetItems()) != 4 {
		t.Fatalf("after removal = %v, want 4 bookmarks", describe(rest.GetItems()))
	}
}

func TestSetBookmarkCodes(t *testing.T) {
	rg := newBookmarkRig(t)
	if _, err := rg.client.DeleteMessage(rg.alice, &chatimv1.DeleteMessageRequest{RoomId: rg.a, Seq: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	cases := []struct {
		name string
		ctx  context.Context
		req  *chatimv1.SetBookmarkRequest
		code codes.Code
	}{
		{"missing message", rg.alice, &chatimv1.SetBookmarkRequest{RoomId: rg.a, Seq: 99, On: true}, codes.NotFound},
		{"deleted message", rg.alice, &chatimv1.SetBookmarkRequest{RoomId: rg.a, Seq: 2, On: true}, codes.FailedPrecondition},
		{"stranger", as(t, "acme", "carol"), &chatimv1.SetBookmarkRequest{RoomId: rg.a, Seq: 1, On: true}, codes.PermissionDenied},
		{"bad room", rg.alice, &chatimv1.SetBookmarkRequest{RoomId: "x", Seq: 1, On: true}, codes.InvalidArgument},
		{"thread", rg.alice, &chatimv1.SetBookmarkRequest{RoomId: rg.a, ThreadRoot: 1, Seq: 1, On: true}, codes.InvalidArgument},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := rg.client.SetBookmark(c.ctx, c.req)
			expectCode(t, err, c.code)
		})
	}
	rg.mark(t, rg.alice, rg.a, 2, false, false)
}

func TestListBookmarksRejectsABadCursorOrLimit(t *testing.T) {
	rg := newBookmarkRig(t)
	short := base64.RawURLEncoding.EncodeToString(make([]byte, 31))
	zero := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for name, req := range map[string]*chatimv1.ListBookmarksRequest{
		"not base64":   {Before: "!!"},
		"short cursor": {Before: short},
		"zero cursor":  {Before: zero},
		"limit":        {Limit: 101},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.ListBookmarks(rg.alice, req)
			expectCode(t, err, codes.InvalidArgument)
		})
	}
}
