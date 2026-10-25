package wsload

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestReconnectDelayStaysWithinBounds(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var low, high bool
	for range 10_000 {
		d := ReconnectDelay(3*time.Second, r.Int64N)
		if d < 0 || d > 3*time.Second {
			t.Fatalf("ReconnectDelay = %v, want 0..3s", d)
		}
		low = low || d < 300*time.Millisecond
		high = high || d > 2700*time.Millisecond
	}
	if !low || !high {
		t.Fatalf("delays do not spread over 0..3s: low %v high %v", low, high)
	}
	if d := ReconnectDelay(0, r.Int64N); d != 0 {
		t.Fatalf("ReconnectDelay(0) = %v", d)
	}
}

func TestOtherURLNeverPicksTheCurrentOne(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for current := range 3 {
		hits := map[int]int{}
		for range 3000 {
			next := OtherURL(3, current, r.IntN)
			if next == current || next < 0 || next >= 3 {
				t.Fatalf("OtherURL(3, %d) = %d", current, next)
			}
			hits[next]++
		}
		if len(hits) != 2 {
			t.Fatalf("OtherURL(3, %d) hits %v, want both other urls", current, hits)
		}
	}
	if got := OtherURL(2, 1, r.IntN); got != 0 {
		t.Fatalf("OtherURL(2, 1) = %d, want 0", got)
	}
	if got := OtherURL(1, 0, r.IntN); got != 0 {
		t.Fatalf("OtherURL(1, 0) = %d, want 0", got)
	}
}
