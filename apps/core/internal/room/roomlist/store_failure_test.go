package roomlist_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/room/roomlist"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestAnInvalidArgumentFromTheStoreStillFailsAsUnavailable(t *testing.T) {
	st := seedMinh(t, 45)
	st.brokenRoom, st.brokenErr = depot, apperr.ErrInvalidArgument
	l := newLister(t, st, 2000)
	_, err := l.List(t.Context(), roomlist.Query{Tenant: acme, User: minh, Folder: domain.FolderMain, Limit: 20})
	if !errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("List = %v, want only ErrUnavailable in the chain", err)
	}
}

func waitForRelease(release <-chan struct{}) func(context.Context, uint64) error {
	return func(ctx context.Context, _ uint64) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func TestOneFailingTailLoadStopsTheQueuedLoads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const rooms = 20
		st := seedMany(t, rooms)
		release := make(chan struct{})
		st.brokenRoom, st.tailHook = manyBase, waitForRelease(release)
		ids := make([]uint64, rooms)
		for i := range ids {
			ids[i] = manyBase + uint64(i)
		}
		st.reset()
		var err error
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _, err = newLister(t, st, 2000).Get(t.Context(), acme, minh, ids)
		}()
		synctest.Wait()
		started, _ := st.snapshot()
		close(release)
		<-done
		if !errors.Is(err, apperr.ErrUnavailable) {
			t.Fatalf("Get = %v, want ErrUnavailable", err)
		}
		finished, _ := st.snapshot()
		if n := finished["TailWindow"]; n < 1 || n > roomlist.TailFills || n != started["TailWindow"] {
			t.Errorf("tail scans = %d (%d before release), want the same 1..%d: loads queued behind the failure must not start", n, started["TailWindow"], roomlist.TailFills)
		}
	})
}

func TestALoadSharedWithAnotherRequestSurvivesTheFirstRequestFailing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := seedMany(t, 2)
		shared, sibling := manyBase, manyBase+1
		begun, release := make(chan struct{}), make(chan struct{})
		wait := waitForRelease(release)
		st.tailHook = func(ctx context.Context, room uint64) error {
			if room == sibling {
				<-begun
				return errBroken
			}
			close(begun)
			return wait(ctx, room)
		}
		l := newLister(t, st, 2000)
		var firstErr, secondErr error
		var second []domain.RoomItem
		var wg sync.WaitGroup
		wg.Go(func() { _, _, firstErr = l.Get(t.Context(), acme, minh, []uint64{shared, sibling}) })
		synctest.Wait()
		wg.Go(func() { second, _, secondErr = l.Get(t.Context(), acme, minh, []uint64{shared}) })
		synctest.Wait()
		close(release)
		wg.Wait()
		if !errors.Is(firstErr, apperr.ErrUnavailable) {
			t.Errorf("first Get = %v, want ErrUnavailable", firstErr)
		}
		if secondErr != nil || len(second) != 1 || second[0].Room.ID != shared {
			t.Errorf("second Get = %v, %v, want room %d", roomsOf(second), secondErr, shared)
		}
		if calls, _ := st.snapshot(); calls["TailWindow"] != 2 {
			t.Errorf("tail scans = %d, want one shared scan and one failing sibling", calls["TailWindow"])
		}
	})
}
