package pgstore_test

import (
	"context"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

type slotHolder interface {
	TryLoadSlot(ctx context.Context, slots int) (func(), bool, error)
}

func takeSlot(t *testing.T, s slotHolder, slots int) (func(), bool) {
	t.Helper()
	release, ok, err := s.TryLoadSlot(context.Background(), slots)
	if err != nil {
		t.Fatalf("TryLoadSlot: %v", err)
	}
	if ok {
		t.Cleanup(release)
	}
	return release, ok
}

func slotHolderPID(t *testing.T, s fixture, slot int) int32 {
	t.Helper()
	var pid int32
	err := s.raw.QueryRow(context.Background(),
		"SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database()) AND classid = 1 AND objid = $1 AND objsubid = 2 AND granted",
		slot).Scan(&pid)
	if err != nil {
		t.Fatalf("find holder of slot %d: %v", slot, err)
	}
	return pid
}

func terminate(t *testing.T, s fixture, pid int32) {
	t.Helper()
	var done bool
	if err := s.raw.QueryRow(context.Background(), "SELECT pg_terminate_backend($1, 5000)", pid).Scan(&done); err != nil || !done {
		t.Fatalf("terminate %d: %v, %v", pid, done, err)
	}
}

func TestAtMostSlotsLoadersHoldASlot(t *testing.T) {
	s := openStore(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var releases []func()
	for range 6 {
		wg.Go(func() {
			release, ok, err := s.TryLoadSlot(context.Background(), 4)
			if err != nil {
				t.Errorf("TryLoadSlot: %v", err)
			}
			if ok {
				t.Cleanup(release)
				mu.Lock()
				releases = append(releases, release)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(releases) != 4 {
		t.Fatalf("%d loaders hold a slot; want 4", len(releases))
	}
	if _, ok := takeSlot(t, s, 4); ok {
		t.Fatalf("a fifth loader got a slot")
	}
	releases[0]()
	releases[0]()
	if _, ok := takeSlot(t, s, 4); !ok {
		t.Fatalf("no slot after one was released")
	}
}

func TestASlotIsFreedWhenTheConnectionDies(t *testing.T) {
	s := openStore(t)
	if _, ok := takeSlot(t, s, 1); !ok {
		t.Fatalf("first loader got no slot")
	}
	terminate(t, s, slotHolderPID(t, s, 0))
	if _, ok := takeSlot(t, s, 1); !ok {
		t.Fatalf("slot still held after its connection died")
	}
}

func TestAFailedUnlockDestroysTheConnection(t *testing.T) {
	s := openStore(t)
	single, err := pgstore.Open(context.Background(), s.uri, pgstore.Options{MaxConns: 1, RevocationKeep: testKeep})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(single.Close)
	ctx := context.Background()

	release, ok := takeSlot(t, single, 1)
	if !ok {
		t.Fatalf("no slot")
	}
	terminate(t, s, slotHolderPID(t, s, 0))
	release()
	if n := single.PoolConns(); n != 0 {
		t.Fatalf("pool holds %d connections after a failed unlock; want 0", n)
	}
	if _, ok := takeSlot(t, single, 1); !ok {
		t.Fatalf("slot not free right after the failed unlock")
	}

	held, left, err := s.ReleaseUnheldSlot(ctx, 3)
	if err != nil {
		t.Fatalf("ReleaseUnheldSlot: %v", err)
	}
	if left != held-1 {
		t.Fatalf("pool connections %d -> %d after an unlock that returned false; want one fewer", held, left)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping after destroying connections: %v", err)
	}
}
