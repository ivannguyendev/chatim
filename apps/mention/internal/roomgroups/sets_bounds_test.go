package roomgroups

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestBackgroundRebuildsAreCappedAndASkippedRoomRetriesLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		gate := f.hold()
		for room := range uint64(MaxRebuilds + 1) {
			expectSet(t, "while capped", of(t, s, 1000+room, 9), false)
		}
		synctest.Wait()
		if n := f.count(); n != MaxRebuilds {
			t.Fatalf("builds while held = %d, want %d", n, MaxRebuilds)
		}
		f.release(gate)
		synctest.Wait()
		expectSet(t, "skipped room", of(t, s, 1000+MaxRebuilds, 9), false)
		synctest.Wait()
		expectSet(t, "skipped room retried", of(t, s, 1000+MaxRebuilds, 9), true)
		if n := f.count(); n != MaxRebuilds+1 {
			t.Fatalf("builds = %d, want %d", n, MaxRebuilds+1)
		}
	})
}

func TestASmallBuildOutlivesTheCallerThatStartedIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		gate := f.hold()
		first, cancel := context.WithCancel(t.Context())
		firstErr := make(chan error)
		go func() {
			_, err := s.Of(first, "acme", smallRoom, 1)
			firstErr <- err
		}()
		synctest.Wait()
		second := make(chan Set)
		go func() { second <- of(t, s, smallRoom, 1) }()
		synctest.Wait()
		cancel()
		if err := <-firstErr; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled caller got %v, want context.Canceled before the build ends", err)
		}
		f.release(gate)
		expectSet(t, "waiter", <-second, true, "sale")
		if n := f.count(); n != 1 {
			t.Fatalf("builds = %d, want the one shared build", n)
		}
	})
}

func TestCloseCancelsASmallBuildInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		f.hold()
		done := make(chan error)
		go func() {
			_, err := s.Of(t.Context(), "acme", smallRoom, 1)
			done <- err
		}()
		synctest.Wait()
		s.Close()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Of after Close = %v, want context.Canceled", err)
		}
	})
}
