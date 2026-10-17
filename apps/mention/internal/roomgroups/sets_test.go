package roomgroups

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

const (
	smallRoom = 101
	bigRoom   = 202
	syncMax   = 3
	ttl       = time.Minute
	rebuildIn = 10 * time.Minute
)

func newSets(t *testing.T) (*Sets, *fakeBuild) {
	t.Helper()
	f := &fakeBuild{groups: map[uint64][]string{smallRoom: {"sale"}, bigRoom: {"dev", "ops"}}}
	s, err := NewSets(Config{SyncMax: syncMax, TTL: ttl, Rebuild: rebuildIn}, f.build, quiet)
	if err != nil {
		t.Fatalf("NewSets: %v", err)
	}
	t.Cleanup(s.Close)
	return s, f
}

func of(t *testing.T, s *Sets, room uint64, members int64) Set {
	t.Helper()
	got, err := s.Of(t.Context(), "acme", room, members)
	if err != nil {
		t.Fatalf("Of(%d): %v", room, err)
	}
	return got
}

func expectSet(t *testing.T, what string, got Set, filtered bool, ids ...string) {
	t.Helper()
	if got.Filtered != filtered || !slices.Equal(idsOf(got), ids) {
		t.Fatalf("%s = %v filtered %v, want %v filtered %v", what, idsOf(got), got.Filtered, ids, filtered)
	}
}

func TestASmallRoomIsBuiltOnAskAndCachedForTheTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		expectSet(t, "first", of(t, s, smallRoom, syncMax), true, "sale")
		f.set(smallRoom, "sale", "hr")
		time.Sleep(ttl - time.Second)
		expectSet(t, "cached", of(t, s, smallRoom, 2), true, "sale")
		time.Sleep(time.Second)
		expectSet(t, "after ttl", of(t, s, smallRoom, 2), true, "hr", "sale")
		if n := f.count(); n != 2 {
			t.Fatalf("builds = %d, want 2", n)
		}
	})
}

func TestForgetDropsASmallRoomAndABuildThatOverlapsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		of(t, s, smallRoom, 1)
		f.set(smallRoom, "hr")
		s.Forget(smallRoom)
		expectSet(t, "after forget", of(t, s, smallRoom, 1), true, "hr")
		s.Forget(smallRoom)
		gate := f.hold()
		done := make(chan Set)
		go func() { done <- of(t, s, smallRoom, 1) }()
		synctest.Wait()
		s.Forget(smallRoom)
		f.release(gate)
		expectSet(t, "overlapped build", <-done, true, "hr")
		of(t, s, smallRoom, 1)
		if n := f.count(); n != 4 {
			t.Fatalf("builds = %d, want the overlapped build left out of the cache", n)
		}
	})
}

func TestConcurrentSmallBuildsOfOneRoomShareOneBuild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		gate := f.hold()
		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() { expectSet(t, "shared", of(t, s, smallRoom, 1), true, "sale") })
		}
		synctest.Wait()
		f.release(gate)
		wg.Wait()
		if n := f.count(); n != 1 {
			t.Fatalf("builds = %d, want 1", n)
		}
	})
}

func TestASmallBuildFailureIsReturnedAndNotCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		f.fail(errDown)
		if _, err := s.Of(t.Context(), "acme", smallRoom, 1); !errors.Is(err, errDown) {
			t.Fatalf("Of = %v, want the build error", err)
		}
		f.fail(nil)
		expectSet(t, "after recovery", of(t, s, smallRoom, 1), true, "sale")
	})
}

func TestABigRoomIsUnfilteredUntilItsBackgroundBuildLands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		gate := f.hold()
		expectSet(t, "first", of(t, s, bigRoom, syncMax+1), false)
		expectSet(t, "while building", of(t, s, bigRoom, syncMax+1), false)
		synctest.Wait()
		f.release(gate)
		synctest.Wait()
		expectSet(t, "built", of(t, s, bigRoom, syncMax+1), true, "dev", "ops")
		if n := f.count(); n != 1 {
			t.Fatalf("builds = %d, want one background build", n)
		}
	})
}

func TestABigRoomServesTheOldSetWhileItRebuilds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		of(t, s, bigRoom, 9)
		synctest.Wait()
		f.set(bigRoom, "ops")
		s.Forget(bigRoom)
		time.Sleep(rebuildIn - time.Second)
		expectSet(t, "before the interval", of(t, s, bigRoom, 9), true, "dev", "ops")
		synctest.Wait()
		time.Sleep(time.Second)
		gate := f.hold()
		expectSet(t, "old set while rebuilding", of(t, s, bigRoom, 9), true, "dev", "ops")
		synctest.Wait()
		f.release(gate)
		synctest.Wait()
		expectSet(t, "rebuilt", of(t, s, bigRoom, 9), true, "ops")
		if n := f.count(); n != 2 {
			t.Fatalf("builds = %d, want 2", n)
		}
	})
}

func TestABigRoomBuildFailureIsCountedAndRetriedOnTheNextAsk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		f.fail(errDown)
		of(t, s, bigRoom, 9)
		synctest.Wait()
		if s.Failures() != 1 {
			t.Fatalf("Failures() = %d, want 1", s.Failures())
		}
		f.fail(nil)
		expectSet(t, "after a failure", of(t, s, bigRoom, 9), false)
		synctest.Wait()
		expectSet(t, "after the retry", of(t, s, bigRoom, 9), true, "dev", "ops")
	})
}

func TestCloseCancelsARunningRebuildAndStopsNewOnes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		f.hold()
		of(t, s, bigRoom, 9)
		synctest.Wait()
		s.Close()
		of(t, s, bigRoom, 9)
		synctest.Wait()
		if n := f.count(); n != 1 || s.Failures() != 1 {
			t.Fatalf("builds = %d failures = %d, want the one canceled build", n, s.Failures())
		}
	})
}
