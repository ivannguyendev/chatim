package dedupe

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestRequestKeysLiveInTheirOwnNamespace(t *testing.T) {
	req := RequestKey(42, "alice", "r1")
	if req.Space != SpaceRequest {
		t.Fatalf("RequestKey space = %v, want SpaceRequest", req.Space)
	}
	if got := req.String(); got != "chatim:req:42:alice:r1" {
		t.Fatalf("request key = %q", got)
	}
	cid := Key{Room: 42, User: "alice", CID: "r1"}
	if cid.Space != SpaceCID {
		t.Fatalf("zero space = %v, want SpaceCID", cid.Space)
	}
	if got := cid.String(); got != "chatim:cid:42:alice:r1" {
		t.Fatalf("cid key = %q", got)
	}
	if req == cid {
		t.Fatal("request key equals the cid key with the same id")
	}
}

func TestNewRequestsNeedsARegistryAndAPositiveTTL(t *testing.T) {
	if _, err := NewRequests(nil, requestTTL); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewRequests(nil registry) = %v, want ErrInvalidArgument", err)
	}
	for _, ttl := range []time.Duration{0, -time.Second} {
		if _, err := NewRequests(&scriptedRegistry{}, ttl); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("NewRequests(ttl %v) = %v, want ErrInvalidArgument", ttl, err)
		}
	}
	if _, err := NewRequests(&scriptedRegistry{}, time.Millisecond); err != nil {
		t.Fatalf("NewRequests(1ms): %v", err)
	}
}

func TestBeginMapsTheRegistryVerdict(t *testing.T) {
	committed := Verdict{Status: Committed, Record: Record{Seq: 3, CreatedAt: time.Now()}}
	cases := []struct {
		name     string
		verdicts []Verdict
		err      error
		want     RequestStatus
	}{
		{"reserved", []Verdict{{Status: Reserved}}, nil, RequestNew},
		{"absent", []Verdict{{Status: Absent}}, nil, RequestNew},
		{"committed", []Verdict{committed}, nil, RequestDone},
		{"pending here", []Verdict{{Status: PendingHere}}, nil, RequestBusy},
		{"pending elsewhere", []Verdict{{Status: PendingElsewhere}}, nil, RequestBusy},
		{"redis error", nil, errors.New("connection refused"), RequestNew},
		{"degraded", nil, ErrDegraded, RequestNew},
		{"batcher closed", nil, ErrBatcherClosed, RequestNew},
		{"registry deadline", nil, context.DeadlineExceeded, RequestNew},
		{"missing verdict", []Verdict{}, nil, RequestNew},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &scriptedRegistry{}
			reg.answer(tc.verdicts, tc.err)
			r := newRequests(t, reg)
			expectBegin(t, r, requestKey("r1"), tc.want)
			reserves, commits, aborts := reg.calls()
			if len(reserves) != 1 || len(reserves[0]) != 1 || reserves[0][0] != requestKey("r1") {
				t.Fatalf("reserves = %v, want one call with the request key", reserves)
			}
			if len(commits)+len(aborts) != 0 {
				t.Fatalf("Begin settled keys: commits %v aborts %v", commits, aborts)
			}
		})
	}
}

func TestFinishAnswersFromMemoryUntilTheTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &scriptedRegistry{}
		reg.answer([]Verdict{{Status: Reserved}}, nil)
		r := newRequests(t, reg)
		k := requestKey("r1")
		rec := Record{Seq: 2, CreatedAt: time.Now()}
		expectBegin(t, r, k, RequestNew)
		r.Finish(t.Context(), k, rec)
		_, commits, _ := reg.calls()
		if len(commits) != 1 || len(commits[0]) != 1 || commits[0][0] != (Entry{Key: k, Record: rec}) {
			t.Fatalf("commits = %v, want one entry for %s", commits, k)
		}
		time.Sleep(requestTTL - time.Nanosecond)
		expectBegin(t, r, k, RequestDone)
		if reserves, _, _ := reg.calls(); len(reserves) != 1 {
			t.Fatalf("a remembered request reached the registry: %v", reserves)
		}
		expectBegin(t, r, requestKey("r2"), RequestNew)
		time.Sleep(time.Nanosecond)
		expectBegin(t, r, k, RequestNew)
		if reserves, _, _ := reg.calls(); len(reserves) != 3 {
			t.Fatalf("an expired request was answered from memory: %v", reserves)
		}
	})
}

func TestACommittedVerdictIsRememberedUntilItsCommandExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &scriptedRegistry{}
		started := time.Now().Add(-requestTTL / 2)
		reg.answer([]Verdict{{Status: Committed, Record: Record{Seq: 1, CreatedAt: started}}}, nil)
		r := newRequests(t, reg)
		k := requestKey("r1")
		expectBegin(t, r, k, RequestDone)
		reg.answer([]Verdict{{Status: Reserved}}, nil)
		time.Sleep(requestTTL/2 - time.Nanosecond)
		expectBegin(t, r, k, RequestDone)
		if reserves, _, _ := reg.calls(); len(reserves) != 1 {
			t.Fatalf("a remembered verdict reached the registry: %v", reserves)
		}
		time.Sleep(time.Nanosecond)
		expectBegin(t, r, k, RequestNew)
		if _, commits, _ := reg.calls(); len(commits) != 0 {
			t.Fatalf("a committed verdict was committed again: %v", commits)
		}
	})
}

func TestCancelAbortsTheReservation(t *testing.T) {
	reg := &scriptedRegistry{}
	reg.answer([]Verdict{{Status: Reserved}}, nil)
	r := newRequests(t, reg)
	k := requestKey("r1")
	expectBegin(t, r, k, RequestNew)
	r.Cancel(t.Context(), k)
	_, commits, aborts := reg.calls()
	if len(aborts) != 1 || len(aborts[0]) != 1 || aborts[0][0] != k || len(commits) != 0 {
		t.Fatalf("aborts = %v, commits = %v, want one abort of %s", aborts, commits, k)
	}
	expectBegin(t, r, k, RequestNew)
}

func TestBeginFailsOnlyWhenTheCallerGaveUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &scriptedRegistry{}
		reg.answer([]Verdict{{Status: Reserved}}, nil)
		r := newRequests(t, reg)
		gone, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, err := r.Begin(gone, requestKey("r1")); !errors.Is(err, context.Canceled) {
			t.Fatalf("Begin with a cancelled caller = %v, want context.Canceled", err)
		}
		reg.hang = true
		late, stop := context.WithTimeout(t.Context(), time.Second)
		defer stop()
		if _, _, err := r.Begin(late, requestKey("r2")); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Begin past the caller deadline = %v, want context.DeadlineExceeded", err)
		}
	})
}
