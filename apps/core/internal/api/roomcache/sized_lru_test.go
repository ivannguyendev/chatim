package roomcache

import "testing"

func TestSizedLRUEvictsLeastRecentlyUsedUntilItFits(t *testing.T) {
	c := newSizedLRU[int, string](100)
	c.put(1, "a", 40)
	c.put(2, "b", 40)
	if _, ok := c.get(1); !ok {
		t.Fatal("entry 1 missing")
	}
	c.put(3, "c", 40)
	if _, ok := c.get(2); ok {
		t.Error("entry 2 kept, want it evicted as least recently used")
	}
	if _, ok := c.get(1); !ok {
		t.Error("entry 1 evicted, want it kept after its read")
	}
	if c.bytes() != 80 || c.entries() != 2 {
		t.Errorf("bytes/entries = %d/%d, want 80/2", c.bytes(), c.entries())
	}
}

func TestSizedLRUReplacesAnEntryAndRefusesOneLargerThanTheLimit(t *testing.T) {
	c := newSizedLRU[int, string](100)
	c.put(1, "a", 40)
	c.put(1, "aa", 70)
	if v, _ := c.get(1); v != "aa" || c.bytes() != 70 {
		t.Errorf("value/bytes = %q/%d, want aa/70", v, c.bytes())
	}
	if c.put(1, "huge", 101) {
		t.Error("put of 101 bytes into 100 succeeded")
	}
	if _, ok := c.get(1); ok || c.bytes() != 0 {
		t.Errorf("an oversized put left the old entry or %d bytes", c.bytes())
	}
	c.put(2, "b", 10)
	c.remove(2)
	c.remove(2)
	if c.bytes() != 0 || c.entries() != 0 {
		t.Errorf("bytes/entries = %d/%d after remove, want 0/0", c.bytes(), c.entries())
	}
}
