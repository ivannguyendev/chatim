package lru_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/pkg/lru"
)

func TestCacheEvictsTheLeastRecentlyUsed(t *testing.T) {
	c := lru.New[string, int](2)
	c.Put("a", 1)
	c.Put("b", 2)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v", v, ok)
	}
	c.Put("c", 3)
	if _, ok := c.Get("b"); ok {
		t.Fatal("b survived although it was least recently used")
	}
	c.Put("a", 10)
	if v, ok := c.Get("a"); !ok || v != 10 {
		t.Fatalf("after update Get(a) = %d, %v", v, ok)
	}
	if v, ok := c.Get("c"); !ok || v != 3 {
		t.Fatalf("Get(c) = %d, %v", v, ok)
	}
}

func TestRemoveForgetsOneKey(t *testing.T) {
	c := lru.New[string, int](4)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Remove("a")
	c.Remove("missing")
	if _, ok := c.Get("a"); ok {
		t.Fatal("a survived Remove")
	}
	if v, ok := c.Get("b"); !ok || v != 2 {
		t.Fatalf("Get(b) = %d, %v", v, ok)
	}
}

func TestLenCountsLiveEntries(t *testing.T) {
	c := lru.New[int, string](3)
	if n := c.Len(); n != 0 {
		t.Fatalf("empty Len = %d", n)
	}
	for i := range 5 {
		c.Put(i, "v")
	}
	if n := c.Len(); n != 3 {
		t.Fatalf("Len after 5 puts with limit 3 = %d", n)
	}
	c.Put(4, "w")
	c.Remove(3)
	if n := c.Len(); n != 2 {
		t.Fatalf("Len after update and remove = %d", n)
	}
}
