package roomgroups

import (
	"testing"
)

func entry(small bool, ids ...string) Entry {
	e := Entry{Groups: map[string]struct{}{}, Small: small}
	for _, id := range ids {
		e.Groups[id] = struct{}{}
	}
	return e
}

func TestCacheForgetsOnlySmallRoomsButRecordsEveryForget(t *testing.T) {
	c, err := NewCache(10)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	mark := c.Mark()
	c.Put(mark, 1, entry(true, "a"))
	c.Put(mark, 2, entry(false, "b"))
	c.Forget(1)
	c.Forget(2)
	if _, ok := c.Get(1); ok {
		t.Fatal("small room 1 still cached after Forget")
	}
	if _, ok := c.Get(2); !ok {
		t.Fatal("big room 2 dropped by Forget, want it kept until its rebuild")
	}
	c.Put(mark, 1, entry(true, "stale"))
	c.Put(mark, 3, entry(false, "c"))
	if _, ok := c.Get(1); ok {
		t.Fatal("a small build older than the forget was cached")
	}
	if _, ok := c.Get(3); !ok {
		t.Fatal("a big rebuild was dropped by an unrelated forget")
	}
}

func TestCacheDropsSmallBuildsOlderThanTheForgetWindow(t *testing.T) {
	c, err := NewCache(10)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	mark := c.Mark()
	for room := uint64(1000); room < 1000+recentForgetRooms; room++ {
		c.Forget(room)
	}
	c.Put(mark, 7, entry(true, "a"))
	if _, ok := c.Get(7); ok {
		t.Fatal("small build cached although the forget window overflowed since its mark")
	}
	if _, err := NewCache(0); err == nil {
		t.Fatal("NewCache(0) succeeded")
	}
}
