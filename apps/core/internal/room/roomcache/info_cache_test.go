package roomcache

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func newInfo(t *testing.T, entries int) *InfoCache {
	t.Helper()
	c, err := NewInfoCache(entries)
	if err != nil {
		t.Fatalf("NewInfoCache: %v", err)
	}
	return c
}

func room(id uint64, name string) domain.Room {
	return domain.Room{ID: id, Name: name, Type: domain.RoomGroup, MemberCount: 3}
}

func TestInfoCacheSplitsHitsAndMisses(t *testing.T) {
	c := newInfo(t, 10)
	c.Put(c.Mark(), room(1, "one"), room(2, "two"))
	hits, misses := c.Get([]uint64{1, 3, 2, 3, 1})
	if len(hits) != 2 || hits[1].Name != "one" || hits[2].Name != "two" {
		t.Errorf("hits = %v, want rooms 1 and 2", hits)
	}
	if !slices.Equal(misses, []uint64{3}) {
		t.Errorf("misses = %v, want [3]", misses)
	}
	expectStats(t, "info", c, 2, 1)
}

func TestInfoCacheEntriesExpireAfterTheTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newInfo(t, 10)
		c.Put(c.Mark(), room(1, "one"))
		time.Sleep(InfoTTL - time.Millisecond)
		if hits, _ := c.Get([]uint64{1}); len(hits) != 1 {
			t.Fatal("entry gone before its TTL")
		}
		time.Sleep(time.Millisecond)
		if _, misses := c.Get([]uint64{1}); !slices.Equal(misses, []uint64{1}) {
			t.Errorf("misses = %v after the TTL, want [1]", misses)
		}
		if c.Len() != 0 {
			t.Errorf("Len = %d, want the expired entry dropped", c.Len())
		}
	})
}

func TestInfoCacheKeepsAtMostItsEntryCount(t *testing.T) {
	c := newInfo(t, 2)
	c.Put(c.Mark(), room(1, "one"), room(2, "two"))
	c.Get([]uint64{1})
	c.Put(c.Mark(), room(3, "three"))
	_, misses := c.Get([]uint64{1, 2, 3})
	if !slices.Equal(misses, []uint64{2}) || c.Len() != 2 {
		t.Errorf("misses = %v, Len = %d; want [2] and 2", misses, c.Len())
	}
}

func TestInfoCacheForgetDropsTheRoomAndRefusesAnOlderRead(t *testing.T) {
	c := newInfo(t, 10)
	c.Put(c.Mark(), room(1, "old"), room(2, "two"))
	readBefore := c.Mark()
	c.Forget(1)
	if _, misses := c.Get([]uint64{1, 2}); !slices.Equal(misses, []uint64{1}) {
		t.Fatalf("misses = %v after Forget(1), want [1]", misses)
	}
	c.Put(readBefore, room(1, "stale"))
	if _, misses := c.Get([]uint64{1}); !slices.Equal(misses, []uint64{1}) {
		t.Error("a read started before the forget was cached")
	}
	c.Put(c.Mark(), room(1, "new"))
	if hits, _ := c.Get([]uint64{1}); hits[1].Name != "new" {
		t.Errorf("room 1 = %q, want the read made after the forget", hits[1].Name)
	}
}

func TestInfoCacheRefusesAReadOlderThanTheForgetsItRemembers(t *testing.T) {
	c := newInfo(t, 10)
	mark := c.Mark()
	for id := uint64(1000); id < 1000+recentForgetRooms-1; id++ {
		c.Forget(id)
	}
	c.Put(mark, room(1, "kept"))
	if hits, _ := c.Get([]uint64{1}); len(hits) != 1 {
		t.Fatal("a read with fewer forgets than remembered was refused")
	}
	c.Forget(1)
	mark = c.Mark()
	for id := uint64(1000); id < 1000+recentForgetRooms; id++ {
		c.Forget(id)
	}
	c.Put(mark, room(2, "unknown"))
	if _, misses := c.Get([]uint64{2}); len(misses) != 1 {
		t.Error("a read older than every remembered forget was cached")
	}
}

func TestCacheConstructorsRejectEmptyBounds(t *testing.T) {
	if _, err := NewInfoCache(0); err == nil {
		t.Error("NewInfoCache(0) succeeded")
	}
	if _, err := NewTailCache(fullTailBytes - 1); err == nil {
		t.Error("NewTailCache below one entry succeeded")
	}
	if _, err := NewSnapshotCache(snapshotBaseBytes - 1); err == nil {
		t.Error("NewSnapshotCache below one entry succeeded")
	}
	if _, err := NewPathCache(0); err == nil {
		t.Error("NewPathCache(0) succeeded")
	}
}
