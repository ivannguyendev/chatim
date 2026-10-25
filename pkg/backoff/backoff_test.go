package backoff_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/pkg/backoff"
)

func TestJitterStaysWithinTheUpperHalf(t *testing.T) {
	for _, d := range []time.Duration{0, time.Nanosecond, time.Millisecond, time.Second} {
		for range 200 {
			if got := backoff.Jitter(d); got < d/2 || got > d {
				t.Fatalf("Jitter(%v) = %v, want within [%v, %v]", d, got, d/2, d)
			}
		}
	}
}

func TestBetweenStaysWithinTheBounds(t *testing.T) {
	lo, hi := 5*time.Second, 30*time.Second
	for range 500 {
		if got := backoff.Between(lo, hi); got < lo || got > hi {
			t.Fatalf("Between(%v, %v) = %v", lo, hi, got)
		}
	}
	if got := backoff.Between(lo, lo); got != lo {
		t.Fatalf("Between of equal bounds = %v, want %v", got, lo)
	}
}

func TestPauseWaitsOrStopsOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		if !backoff.Pause(t.Context(), time.Second) || time.Since(start) != time.Second {
			t.Fatalf("Pause returned early after %v", time.Since(start))
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if backoff.Pause(ctx, time.Hour) {
			t.Fatal("Pause ignored a cancelled context")
		}
	})
}
