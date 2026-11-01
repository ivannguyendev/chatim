package pgstore

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestAdminSlotsDefaultWhenUnset(t *testing.T) {
	for _, n := range []int{0, -3} {
		if got := cap(newAdminSlots(n)); got != defaultAdminDBSlots {
			t.Fatalf("newAdminSlots(%d) holds %d; want %d", n, got, defaultAdminDBSlots)
		}
	}
	if got := cap(newAdminSlots(7)); got != 7 {
		t.Fatalf("newAdminSlots(7) holds %d; want 7", got)
	}
}

func TestAdminSlotsWaitUntilOneIsReleased(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		slots := newAdminSlots(2)
		first, err := slots.enter(ctx)
		if err != nil {
			t.Fatalf("enter 1: %v", err)
		}
		if _, err := slots.enter(ctx); err != nil {
			t.Fatalf("enter 2: %v", err)
		}
		entered := make(chan func(), 1)
		go func() {
			release, err := slots.enter(ctx)
			if err != nil {
				t.Errorf("enter 3: %v", err)
			}
			entered <- release
		}()
		synctest.Wait()
		select {
		case <-entered:
			t.Fatal("the third caller entered while both slots were held")
		default:
		}
		first()
		synctest.Wait()
		select {
		case release := <-entered:
			release()
		default:
			t.Fatal("the third caller did not enter after a slot was released")
		}
	})
}

func TestAdminSlotsWaitEndsWithTheContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		slots := newAdminSlots(1)
		if _, err := slots.enter(context.Background()); err != nil {
			t.Fatalf("enter 1: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := slots.enter(ctx)
			done <- err
		}()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("enter returned %v before the context ended", err)
		default:
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("enter after cancel = %v; want context.Canceled", err)
		}
		cancelled, stop := context.WithCancel(context.Background())
		stop()
		free := newAdminSlots(1)
		if _, err := free.enter(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("enter with a done context = %v; want context.Canceled", err)
		}
		if len(free) != 0 {
			t.Fatal("a done context took a free slot")
		}
	})
}
