package roomlist_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/room/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/room/roomlist"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const chattyBase uint64 = 60_000

func seedChatty(t *testing.T, rooms int, messages uint64) *countingStore {
	t.Helper()
	c := newWorld()
	for i := range rooms {
		room := chattyBase + uint64(i)
		c.create(t, group(room, acme, fmt.Sprintf("Chatty %d", i)), person(room, acme, lan, domain.RoleOwner, 0), person(room, acme, minh, domain.RoleMember, 0))
		for seq := uint64(1); seq <= messages; seq++ {
			c.post(t, room, acme, seq, lan, clock(9, i).Add(time.Duration(seq)*time.Second))
		}
	}
	return c
}

func expectEveryPreview(t *testing.T, items []domain.RoomItem, rooms int, seq uint64) {
	t.Helper()
	if len(items) != rooms {
		t.Fatalf("items = %v, want %d rooms", roomsOf(items), rooms)
	}
	for _, it := range items {
		if it.Preview.Seq != seq || it.Preview.Text != fmt.Sprintf("tin %d", seq) {
			t.Errorf("room %d preview = %d %q, want seq %d", it.Room.ID, it.Preview.Seq, it.Preview.Text, seq)
		}
	}
}

func TestAColdPageReadsEveryRoomWindowAndOneBatchOfPreviews(t *testing.T) {
	st := seedChatty(t, 20, 12)
	l := newLister(t, st, 2000)
	p := list(t, l, acme, domain.FolderMain, nil, 20)
	st.expectCalls(t, map[string]int{"CountRooms": 1, "JoinedRoomOrder": 1, "RoomInfos": 1, "TailWindow": 20, "TailPreviews": 1, "HiddenInRooms": 1})
	if n := st.previewKeys(); n != 20*roomcache.TailPreviews {
		t.Errorf("preview keys = %d, want %d", n, 20*roomcache.TailPreviews)
	}
	expectEveryPreview(t, p.Items, 20, 12)
	st.reset()
	warm := list(t, l, acme, domain.FolderMain, nil, 20)
	st.expectCalls(t, map[string]int{"JoinedRoomOrder": 1, "HiddenInRooms": 1})
	expectEveryPreview(t, warm.Items, 20, 12)
}

func TestANewMessageReadsOnlyItsOwnPreview(t *testing.T) {
	st := seedChatty(t, 20, 12)
	l := newLister(t, st, 2000)
	list(t, l, acme, domain.FolderMain, nil, 20)
	st.post(t, chattyBase, acme, 13, lan, clock(11, 0))
	st.reset()
	p := list(t, l, acme, domain.FolderMain, nil, 20)
	calls, _ := st.snapshot()
	if calls["TailWindow"] != 1 || calls["TailPreviews"] != 1 || st.previewKeys() != 1 {
		t.Errorf("calls = %v with %d preview keys, want one scan and one preview key", calls, st.previewKeys())
	}
	if first := p.Items[0]; first.Room.ID != chattyBase || first.Preview.Seq != 13 || first.Unread.Count != 13 {
		t.Errorf("first item = room %d preview %d unread %d, want room %d preview 13 unread 13", first.Room.ID, first.Preview.Seq, first.Unread.Count, chattyBase)
	}
}

func TestConcurrentPagesShareOneWindowLoadPerRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := seedChatty(t, 20, 12)
		release := make(chan struct{})
		st.tailHook = waitForRelease(release)
		l := newLister(t, st, 2000)
		pages := make([]roomlist.Page, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range pages {
			wg.Go(func() {
				pages[i], errs[i] = l.List(t.Context(), roomlist.Query{Tenant: acme, User: minh, Folder: domain.FolderMain, Limit: 20})
			})
		}
		synctest.Wait()
		close(release)
		wg.Wait()
		for i := range pages {
			if errs[i] != nil {
				t.Fatalf("page %d: %v", i, errs[i])
			}
			expectEveryPreview(t, pages[i].Items, 20, 12)
		}
		calls, _ := st.snapshot()
		if calls["TailWindow"] != 20 || calls["TailPreviews"] < 1 || calls["TailPreviews"] > 2 {
			t.Errorf("calls = %v, want 20 window loads and at most one preview batch per page", calls)
		}
	})
}

func TestAFailedPreviewBatchFailsTheCallAndTheNextCallReadsItAgain(t *testing.T) {
	st := seedChatty(t, 3, 12)
	st.previewErr = errBroken
	l := newLister(t, st, 2000)
	if _, err := l.List(t.Context(), roomlist.Query{Tenant: acme, User: minh, Folder: domain.FolderMain, Limit: 20}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("List = %v, want ErrUnavailable", err)
	}
	st.previewErr = nil
	st.reset()
	p := list(t, l, acme, domain.FolderMain, nil, 20)
	calls, _ := st.snapshot()
	if calls["TailWindow"] != 0 || calls["TailPreviews"] != 1 {
		t.Errorf("calls = %v, want the cached windows and one preview batch", calls)
	}
	expectEveryPreview(t, p.Items, 3, 12)
}
