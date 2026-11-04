package mongostore

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

type dueResult struct {
	kid string
	err error
}

func TestActivateDueSigningKeyPicksTheNewestDueKeyNewerThanTheActiveOne(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	if kid, err := s.ActivateDueSigningKey(t.Context(), time.Hour); err != nil || kid != "" {
		t.Fatalf("ActivateDue with no key = %q, %v; want empty", kid, err)
	}
	now := serverNow(t, s)
	insertPendingKey(t, s, "k1", now.Add(-3*time.Hour))
	insertPendingKey(t, s, "k2", now.Add(-2*time.Hour))
	insertPendingKey(t, s, "k3", now.Add(-10*time.Minute))
	kid, err := s.ActivateDueSigningKey(t.Context(), time.Hour)
	if err != nil || kid != "k2" {
		t.Fatalf("ActivateDue = %q, %v; want k2", kid, err)
	}
	keys := signingKeysByID(t, s)
	if keys["k1"].State != store.KeyRetired || keys["k2"].State != store.KeyActive || keys["k3"].State != store.KeyPending {
		t.Fatalf("states k1 %d k2 %d k3 %d; want retired, active, pending", keys["k1"].State, keys["k2"].State, keys["k3"].State)
	}
	if claim, _ := readClaim(t, s); claim.KeyID != "k2" {
		t.Fatalf("claim = %+v; want k2", claim)
	}
	if kid, err := s.ActivateDueSigningKey(t.Context(), time.Hour); err != nil || kid != "" {
		t.Fatalf("ActivateDue with only k3 not due = %q, %v; want empty", kid, err)
	}
	insertPendingKey(t, s, "k0", now.Add(-150*time.Minute))
	if kid, err := s.ActivateDueSigningKey(t.Context(), time.Hour); err != nil || kid != "" {
		t.Fatalf("ActivateDue with a due key older than the active one = %q, %v; want empty", kid, err)
	}
	if active := activeKeyIDs(t, s); !slices.Equal(active, []string{"k2"}) {
		t.Fatalf("active keys = %v; want only k2", active)
	}
}

func TestActivateDueSigningKeyRaceLeavesExactlyOneActiveKey(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	insertPendingKey(t, s, "k1", serverNow(t, s).Add(-2*time.Hour))
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	holdBeforeCommit(t, func() {
		arrived <- struct{}{}
		<-release
	})
	results := make(chan dueResult, 2)
	for range 2 {
		go func() {
			kid, err := s.ActivateDueSigningKey(context.Background(), time.Hour)
			results <- dueResult{kid: kid, err: err}
		}()
	}
	waitFor(t, arrived, "first activation at the commit barrier")
	loser := waitFor(t, results, "conflicting activation")
	if !errors.Is(loser.err, apperr.ErrUnavailable) && (loser.err != nil || loser.kid != "") {
		t.Fatalf("conflicting ActivateDue = %q, %v; want unavailable or empty", loser.kid, loser.err)
	}
	close(release)
	if winner := waitFor(t, results, "held activation"); winner.err != nil || winner.kid != "k1" {
		t.Fatalf("held ActivateDue = %q, %v; want k1", winner.kid, winner.err)
	}
	if active := activeKeyIDs(t, s); !slices.Equal(active, []string{"k1"}) {
		t.Fatalf("active keys = %v; want only k1", active)
	}
	if claim, _ := readClaim(t, s); claim != (claimDoc{KeyID: "k1", Ver: 1}) {
		t.Fatalf("claim = %+v; want k1 ver 1", claim)
	}
}
