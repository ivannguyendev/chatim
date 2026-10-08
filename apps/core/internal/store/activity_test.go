package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestHourBucketCountsWholeUTCHours(t *testing.T) {
	at := time.Date(2026, 10, 5, 9, 59, 59, 0, time.FixedZone("ICT", 7*3600))
	if got, want := store.HourBucket(at), at.UTC().Unix()/3600; got != want {
		t.Fatalf("HourBucket(%v) = %d, want %d", at, got, want)
	}
	if store.HourBucket(at.Add(-59*time.Minute)) != store.HourBucket(at) {
		t.Fatalf("HourBucket splits one UTC hour")
	}
	if store.HourBucket(at.Add(time.Second)) != store.HourBucket(at)+1 {
		t.Fatalf("HourBucket does not move at the top of the hour")
	}
}

func TestActiveQueryValidate(t *testing.T) {
	from := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		q    store.ActiveQuery
		ok   bool
	}{
		{"one hour", store.ActiveQuery{From: from, To: from.Add(time.Hour), Limit: 1}, true},
		{"single instant at the max limit", store.ActiveQuery{From: from, To: from, Limit: store.MaxActiveLimit}, true},
		{"zero limit", store.ActiveQuery{From: from, To: from, Limit: 0}, false},
		{"limit above max", store.ActiveQuery{From: from, To: from, Limit: store.MaxActiveLimit + 1}, false},
		{"no start", store.ActiveQuery{To: from, Limit: 1}, false},
		{"end before start", store.ActiveQuery{From: from, To: from.Add(-time.Second), Limit: 1}, false},
	}
	for _, tt := range tests {
		err := tt.q.Validate()
		if tt.ok != (err == nil) || (err != nil && !errors.Is(err, apperr.ErrInvalidArgument)) {
			t.Errorf("%s: Validate() = %v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}
