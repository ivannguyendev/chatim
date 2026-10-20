package roomcache

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestReturnedTailsDoNotShareTheCachedSlices(t *testing.T) {
	c := newTails(t, 1<<22)
	scribble := func(name string, got Tail) {
		got.Window[0].From, got.Previews[0].Text = "mallory", "mallory"
		cached, _ := c.Lookup(activity(777, 0, editedAt))
		if cached.Window[0].From != "lan" || cached.Previews[0].Text != "text" {
			t.Errorf("%s: mutating the returned tail changed the cache", name)
		}
	}
	scribble("Replace", c.Replace(777, tailOf(1, 44)))
	got, _ := c.Lookup(activity(777, 44, editedAt))
	scribble("Lookup", got)
	merged, _ := c.Merge(777, tailOf(45, 46))
	scribble("Merge", merged)
	f := &fakeFetch{result: func(after uint64) Tail { return tailOf(after+1, 47) }}
	loaded, err := c.Load(t.Context(), activity(777, 47, editedAt), f.fetch)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	scribble("Load extend", loaded)
	loaded, _ = c.Load(t.Context(), activity(777, 47, editedAt), f.fetch)
	scribble("Load fresh", loaded)
}

func TestAnExtendThatFetchesAFullWindowReplacesTheTail(t *testing.T) {
	c := newTails(t, 1<<22)
	c.Replace(777, tailOf(1, 44))
	f := &fakeFetch{result: func(uint64) Tail { return tailOf(701, 1000) }}
	got, err := c.Load(t.Context(), activity(777, 990, editedAt), f.fetch)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if calls := f.made(); len(calls) != 1 || calls[0].after != 44 {
		t.Errorf("fetches = %v, want one after seq 44", calls)
	}
	expectSeqs(t, "window", windowSeqs(got), 1000, 701)
	cached, state := c.Lookup(activity(777, 1000, editedAt))
	if state != TailFresh || cached.LastSeq != 1000 {
		t.Fatalf("state %d LastSeq %d, want the fetched tail cached fresh at 1000", state, cached.LastSeq)
	}
	expectSeqs(t, "cached window", windowSeqs(cached), 1000, 701)
	expectSeqs(t, "cached previews", previewSeqs(cached), 1000, 991)
}

func TestTailsReloadAfterTheirTTLEvenWhenMerged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTails(t, 1<<22)
		c.Replace(777, tailOf(1, 44))
		time.Sleep(TailTTL / 2)
		if _, ok := c.Merge(777, tailOf(45, 45)); !ok {
			t.Fatal("Merge refused a live entry")
		}
		time.Sleep(TailTTL/2 - time.Millisecond)
		if _, state := c.Lookup(activity(777, 45, editedAt)); state != TailFresh {
			t.Fatalf("state %d before the TTL, want fresh", state)
		}
		time.Sleep(time.Millisecond)
		if _, state := c.Lookup(activity(777, 45, editedAt)); state != TailReload {
			t.Errorf("state %d after the TTL, want reload although a merge happened", state)
		}
		if _, ok := c.Merge(777, tailOf(46, 46)); ok {
			t.Error("Merge into an expired entry succeeded")
		}
		f := &fakeFetch{result: func(uint64) Tail { return tailOf(1, 46) }}
		if _, err := c.Load(t.Context(), activity(777, 46, editedAt), f.fetch); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if calls := f.made(); len(calls) != 1 || calls[0].after != 0 {
			t.Errorf("fetches = %v, want one full reload", calls)
		}
		if _, state := c.Lookup(activity(777, 46, editedAt)); state != TailFresh {
			t.Errorf("state %d after the reload, want fresh", state)
		}
	})
}
