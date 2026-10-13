package roomcache

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

var minh = SnapshotKey{Tenant: "acme", User: "minh", Folder: domain.FolderMain}

func sortKeys(n int) []domain.RoomSortKey {
	base := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	keys := []domain.RoomSortKey{{Pinned: true, PinnedAt: base, SortAt: base, Room: 999}}
	for i := 1; i < n; i++ {
		keys = append(keys, domain.RoomSortKey{SortAt: base.Add(time.Duration(i) * time.Minute), Room: uint64(i)})
	}
	return keys
}

func newSnapshots(t *testing.T) *SnapshotCache {
	t.Helper()
	c, err := NewSnapshotCache(1 << 20)
	if err != nil {
		t.Fatalf("NewSnapshotCache: %v", err)
	}
	return c
}

func rooms(keys []domain.RoomSortKey) []uint64 {
	out := make([]uint64, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Room)
	}
	return out
}

func TestSnapshotPagesInSortOrderAfterTheCursor(t *testing.T) {
	c := newSnapshots(t)
	c.Store(minh, sortKeys(6))
	page, more, ok := c.Page(minh, nil, 3)
	if !ok || !more || !equalRooms(rooms(page), 999, 5, 4) {
		t.Fatalf("first page = %v more %v ok %v, want [999 5 4] more", rooms(page), more, ok)
	}
	page, more, _ = c.Page(minh, &page[2], 3)
	if more || !equalRooms(rooms(page), 3, 2, 1) {
		t.Errorf("second page = %v more %v, want [3 2 1] and the end", rooms(page), more)
	}
	gone := domain.RoomSortKey{SortAt: sortKeys(6)[4].SortAt.Add(time.Second), Room: 77}
	if page, _, _ = c.Page(minh, &gone, 2); !equalRooms(rooms(page), 4, 3) {
		t.Errorf("page after a cursor not in the snapshot = %v, want [4 3]", rooms(page))
	}
	expectStats(t, "snapshot", c, 3, 0)
}

func TestSnapshotsExpireAndAreKeptPerUserAndFolder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newSnapshots(t)
		c.Store(minh, sortKeys(3))
		archived := minh
		archived.Folder = domain.FolderArchived
		if _, _, ok := c.Page(archived, nil, 20); ok {
			t.Error("another folder read the main snapshot")
		}
		time.Sleep(SnapshotTTL - time.Millisecond)
		if _, _, ok := c.Page(minh, nil, 20); !ok {
			t.Fatal("snapshot gone before its TTL")
		}
		time.Sleep(time.Millisecond)
		if _, _, ok := c.Page(minh, nil, 20); ok {
			t.Error("snapshot served after its TTL")
		}
		if c.Bytes() != 0 {
			t.Errorf("Bytes = %d after expiry, want 0", c.Bytes())
		}
		expectStats(t, "snapshot", c, 1, 2)
	})
}

func TestSnapshotStoreSortsAndForgetDrops(t *testing.T) {
	c := newSnapshots(t)
	keys := sortKeys(4)
	c.Store(minh, []domain.RoomSortKey{keys[1], keys[3], keys[0], keys[2]})
	page, _, _ := c.Page(minh, nil, 10)
	if !equalRooms(rooms(page), 999, 3, 2, 1) {
		t.Errorf("page = %v, want the stored keys in sort order", rooms(page))
	}
	want := snapshotBaseBytes + len("acme") + len("minh") + 4*snapshotKeyBytes
	if c.Bytes() != want {
		t.Errorf("Bytes = %d, want %d", c.Bytes(), want)
	}
	c.Forget(minh)
	if _, _, ok := c.Page(minh, nil, 10); ok {
		t.Error("snapshot served after Forget")
	}
}

func equalRooms(got []uint64, want ...uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
