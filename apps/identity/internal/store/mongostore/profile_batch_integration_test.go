package mongostore

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const (
	batchUsers     = 2000
	batchRacers    = 50
	maxBatchShare  = 4
	racingUserName = "racer"
)

func createConcurrently(t *testing.T, s *Store, writes []store.ProfileWrite) (written int) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan bool, len(writes))
	errs := make(chan error, len(writes))
	var wg sync.WaitGroup
	for _, w := range writes {
		wg.Go(func() {
			<-start
			ok, err := s.WriteProfile(context.Background(), w)
			if err != nil {
				errs <- err
				return
			}
			results <- ok
		})
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("WriteProfile: %v", err)
	}
	for ok := range results {
		if ok {
			written++
		}
	}
	return written
}

func TestWriteProfileBatchesConcurrentCreates(t *testing.T) {
	s := testDatabase(t)
	writes := make([]store.ProfileWrite, batchUsers)
	for i := range writes {
		writes[i] = jitWrite(fmt.Sprintf("u%04d", i), "User", "", nil, itAt)
	}
	if written := createConcurrently(t, s, writes); written != batchUsers {
		t.Fatalf("written creates = %d; want %d", written, batchUsers)
	}
	if n := countUsers(t, s); n != batchUsers {
		t.Fatalf("users = %d; want %d", n, batchUsers)
	}
	h := s.JITUserBatchDocs()
	if h.Sum != batchUsers || h.Count*maxBatchShare > batchUsers {
		t.Fatalf("inserts = %d for %v docs; want at most %d inserts for %d docs", h.Count, h.Sum, batchUsers/maxBatchShare, batchUsers)
	}
	t.Logf("%d users in %d inserts", batchUsers, h.Count)
}

func TestWriteProfileRacingCreatesInOneBatchWriteOnce(t *testing.T) {
	s := testDatabase(t)
	writes := make([]store.ProfileWrite, batchRacers)
	for i := range writes {
		writes[i] = jitWrite(racingUserName, fmt.Sprintf("Racer %d", i), "", nil, itAt)
	}
	if written := createConcurrently(t, s, writes); written != 1 {
		t.Fatalf("written creates = %d; want exactly 1", written)
	}
	if n := countUsers(t, s); n != 1 {
		t.Fatalf("users = %d; want 1", n)
	}
}
