package openloop

import (
	"sync"
	"testing"
)

func draw(t *testing.T, p *Picker, n, rooms int) []int {
	t.Helper()
	hits := make([]int, rooms)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range n / 4 {
				i := p.Next()
				mu.Lock()
				hits[i]++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return hits
}

func TestUniformPickerSpreadsEvenly(t *testing.T) {
	p, err := NewPicker(10, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i, h := range draw(t, p, 100_000, 10) {
		if h < 9_000 || h > 11_000 {
			t.Fatalf("room %d picked %d times out of 100000, want about 10000", i, h)
		}
	}
}

func TestZipfPickerFavoursTheFirstRooms(t *testing.T) {
	p, err := NewPicker(1000, 1.2, 1)
	if err != nil {
		t.Fatal(err)
	}
	hits := draw(t, p, 100_000, 1000)
	top := hits[0] + hits[1] + hits[2] + hits[3] + hits[4] + hits[5] + hits[6] + hits[7] + hits[8] + hits[9]
	if hits[0] <= hits[1] || hits[1] <= hits[9] || top < 50_000 {
		t.Fatalf("first rooms got %v (top 10 = %d of 100000), want a skew towards room 0", hits[:10], top)
	}
}

func TestNewPickerRejectsBadShapes(t *testing.T) {
	for _, tc := range []struct {
		rooms int
		s     float64
	}{{0, 0}, {10, 0.5}, {10, 1}, {10, -2}} {
		if _, err := NewPicker(tc.rooms, tc.s, 1); err == nil {
			t.Fatalf("NewPicker(%d, %v) accepted it", tc.rooms, tc.s)
		}
	}
}
