package storetest

import (
	"context"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type directCase struct {
	name string
	run  func(t *testing.T, s store.DirectRooms)
}

func RunDirectRooms(t *testing.T, open func(t *testing.T) store.DirectRooms) {
	t.Helper()
	cases := []directCase{
		{"FirstClaimStoresTheCandidate", directFirstClaim},
		{"LaterClaimsGetTheFirstRoomInEitherOrder", directLaterClaims},
		{"PairsAndTenantsStayApart", directApart},
		{"ParallelClaimsGetOneRoom", directParallel},
		{"RepointMovesOnlyFromTheStoredRoom", directRepoint},
		{"InvalidInputIsRejected", directInvalid},
		{"CancelledContext", directCancelled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func mustClaim(t *testing.T, s store.DirectRooms, tenantID, a, b string, candidate uint64) uint64 {
	t.Helper()
	got, err := s.Claim(t.Context(), tenantID, a, b, candidate, baseTime)
	if err != nil {
		t.Fatalf("Claim(%s, %s, %s, %d): %v", tenantID, a, b, candidate, err)
	}
	return got
}

func expectClaim(t *testing.T, s store.DirectRooms, tenantID, a, b string, candidate, want uint64) {
	t.Helper()
	if got := mustClaim(t, s, tenantID, a, b, candidate); got != want {
		t.Fatalf("Claim(%s, %s, %s, %d) = %d, want %d", tenantID, a, b, candidate, got, want)
	}
}

func directFirstClaim(t *testing.T, s store.DirectRooms) {
	expectClaim(t, s, tenant, "lan", "minh", 101, 101)
}

func directLaterClaims(t *testing.T, s store.DirectRooms) {
	expectClaim(t, s, tenant, "lan", "minh", 101, 101)
	expectClaim(t, s, tenant, "minh", "lan", 202, 101)
	expectClaim(t, s, tenant, "lan", "minh", 303, 101)
}

func directApart(t *testing.T, s store.DirectRooms) {
	expectClaim(t, s, tenant, "lan", "minh", 101, 101)
	expectClaim(t, s, tenant, "lan", "hoa", 202, 202)
	expectClaim(t, s, "other", "lan", "minh", 303, 303)
	expectClaim(t, s, tenant, "Lan", "minh", 404, 404)
}

func directParallel(t *testing.T, s store.DirectRooms) {
	const openers uint64 = 16
	got := make([]uint64, openers)
	errs := make([]error, openers)
	var wg sync.WaitGroup
	for i := range openers {
		wg.Go(func() {
			a, b := "lan", "minh"
			if i%2 == 1 {
				a, b = b, a
			}
			got[i], errs[i] = s.Claim(t.Context(), tenant, a, b, 1000+i, baseTime)
		})
	}
	wg.Wait()
	for i, room := range got {
		if errs[i] != nil || room != got[0] || room < 1000 || room >= 1000+openers {
			t.Fatalf("opener %d got room %d (%v), all got %v; want one candidate for all", i, room, errs[i], got)
		}
	}
}

func directRepoint(t *testing.T, s store.DirectRooms) {
	expectClaim(t, s, tenant, "lan", "minh", 101, 101)
	steps := []struct {
		old, next uint64
		want      bool
	}{{999, 505, false}, {101, 505, true}, {101, 606, false}}
	for _, st := range steps {
		ok, err := s.Repoint(t.Context(), tenant, "minh", "lan", st.old, st.next)
		if err != nil || ok != st.want {
			t.Fatalf("Repoint(%d -> %d) = %v, %v; want %v", st.old, st.next, ok, err, st.want)
		}
	}
	expectClaim(t, s, tenant, "lan", "minh", 707, 505)
	ok, err := s.Repoint(t.Context(), tenant, "lan", "hoa", 0, 808)
	if err == nil && ok {
		t.Fatal("Repoint of an unclaimed pair moved it")
	}
}

func directInvalid(t *testing.T, s store.DirectRooms) {
	ctx := t.Context()
	_, err := s.Claim(ctx, tenant, "lan", "lan", 101, baseTime)
	assertErrorIs(t, "Claim(self)", err, apperr.ErrInvalidArgument)
	_, err = s.Claim(ctx, tenant, "lan", "minh", 0, baseTime)
	assertErrorIs(t, "Claim(zero room)", err, apperr.ErrInvalidArgument)
	_, err = s.Claim(ctx, "Acme", "lan", "minh", 101, baseTime)
	assertErrorIs(t, "Claim(bad tenant)", err, apperr.ErrInvalidArgument)
	_, err = s.Repoint(ctx, tenant, "lan", "minh", 101, 0)
	assertErrorIs(t, "Repoint(zero next)", err, apperr.ErrInvalidArgument)
	_, err = s.Repoint(ctx, tenant, "lan", "l n", 101, 202)
	assertErrorIs(t, "Repoint(bad user)", err, apperr.ErrInvalidArgument)
	expectClaim(t, s, tenant, "lan", "minh", 303, 303)
}

func directCancelled(t *testing.T, s store.DirectRooms) {
	ctx := cancelledContext(t)
	_, err := s.Claim(ctx, tenant, "lan", "minh", 101, baseTime)
	assertErrorIs(t, "Claim", err, context.Canceled)
	_, err = s.Repoint(ctx, tenant, "lan", "minh", 101, 202)
	assertErrorIs(t, "Repoint", err, context.Canceled)
	expectClaim(t, s, tenant, "lan", "minh", 303, 303)
}
