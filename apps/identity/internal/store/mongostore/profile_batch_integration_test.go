package mongostore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
)

const (
	batchUsers    = 2000
	batchRacers   = 50
	maxBatchShare = 4
	racingSub     = "racer"
)

func linkConcurrently(t *testing.T, s *Store, users []store.NewUser) []store.LinkedUser {
	t.Helper()
	start := make(chan struct{})
	out := make([]linkOutcome, len(users))
	var wg sync.WaitGroup
	for i, u := range users {
		wg.Go(func() {
			<-start
			linked, err := s.LinkUser(context.Background(), u)
			out[i] = linkOutcome{linked, err}
		})
	}
	close(start)
	wg.Wait()
	linked := make([]store.LinkedUser, len(users))
	for i, o := range out {
		if o.linked.Result == store.LinkPending && errors.Is(o.err, apperr.ErrUnavailable) {
			o.linked, o.err = s.LinkUser(t.Context(), users[i])
		}
		if o.err != nil {
			t.Fatalf("LinkUser %d = %+v, %v", i, o.linked, o.err)
		}
		linked[i] = o.linked
	}
	return linked
}

func wantFewBatches(t *testing.T, name string, h metrics.Histogram) {
	t.Helper()
	if h.Sum != batchUsers || h.Count*maxBatchShare > batchUsers {
		t.Fatalf("%s inserts = %d for %v docs; want at most %d inserts for %d docs", name, h.Count, h.Sum, batchUsers/maxBatchShare, batchUsers)
	}
	t.Logf("%s: %d docs in %d inserts", name, batchUsers, h.Count)
}

func TestLinkUserBatchesConcurrentNewSubjects(t *testing.T) {
	s := testDatabase(t)
	users := make([]store.NewUser, batchUsers)
	for i := range users {
		users[i] = subjectUser("shop", fmt.Sprintf("u%04d", i), "User", "")
	}
	for i, got := range linkConcurrently(t, s, users) {
		if got != (store.LinkedUser{ID: users[i].CandidateID, Result: store.LinkCreated}) {
			t.Fatalf("LinkUser %d = %+v; want %s created", i, got, users[i].CandidateID)
		}
	}
	if n := countUsers(t, s); n != batchUsers {
		t.Fatalf("users = %d; want %d", n, batchUsers)
	}
	wantFewBatches(t, "links", s.JITLinkBatchDocs())
	wantFewBatches(t, "users", s.JITUserBatchDocs())
}

func TestLinkUserRacingSubjectsInOneBatchShareOneUser(t *testing.T) {
	s := testDatabase(t)
	users := make([]store.NewUser, batchRacers)
	for i := range users {
		users[i] = subjectUser("shop", fmt.Sprintf("cand%02d", i), fmt.Sprintf("Racer %d", i), "")
		users[i].Sub = racingSub
	}
	linked := linkConcurrently(t, s, users)
	created := 0
	for _, got := range linked {
		if got.ID != linked[0].ID {
			t.Fatalf("racers got ids %q and %q; want one shared id", linked[0].ID, got.ID)
		}
		if got.Result == store.LinkCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created links = %d; want exactly 1", created)
	}
	if n := countUsers(t, s); n != 1 {
		t.Fatalf("users = %d; want 1", n)
	}
}
