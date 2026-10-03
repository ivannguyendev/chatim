package openloop

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type firing struct {
	shot Shot
	at   time.Time
}

type recorder struct {
	mu       sync.Mutex
	fired    []firing
	inflight int
	peak     int
	holdup   time.Duration
}

func (r *recorder) fire(ctx context.Context, s Shot) {
	r.mu.Lock()
	r.inflight++
	r.peak = max(r.peak, r.inflight)
	r.fired = append(r.fired, firing{shot: s, at: time.Now()})
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.inflight--
		r.mu.Unlock()
	}()
	if r.holdup > 0 {
		t := time.NewTimer(r.holdup)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
		}
	}
}

func TestRunFiresEveryShotAtItsScheduledTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := Plan{Rate: 1000, Warmup: 100 * time.Millisecond, Duration: 400 * time.Millisecond, MaxInflight: 100}
		rec := &recorder{}
		start := time.Now()
		c := Run(t.Context(), p, rec.fire)
		if c.All != (Window{Due: 500, Issued: 500}) || c.Measured != (Window{Due: 400, Issued: 400}) {
			t.Fatalf("counts = %+v, want 500 due and issued, 400 of them measured", c)
		}
		if c.Lag.Count != 400 || c.Lag.Max != 0 || c.Elapsed != 499*time.Millisecond {
			t.Fatalf("lag %v after %v, want 400 measured shots on time and the last at 499ms", c.Lag, c.Elapsed)
		}
		seen := make(map[int]bool, len(rec.fired))
		for _, f := range rec.fired {
			want := start.Add(time.Duration(f.shot.Index) * time.Millisecond)
			if !f.shot.Scheduled.Equal(want) || !f.at.Equal(want) || f.shot.Measured != (f.shot.Index >= 100) || seen[f.shot.Index] {
				t.Fatalf("shot %+v fired at +%v, want once at +%v", f.shot, f.at.Sub(start), want.Sub(start))
			}
			seen[f.shot.Index] = true
		}
		if len(seen) != 500 {
			t.Fatalf("fired %d distinct shots, want 500", len(seen))
		}
	})
}

func TestPlanSpreadsFractionalIntervalsWithoutDrift(t *testing.T) {
	p := Plan{Rate: 3, Warmup: 0, Duration: 2 * time.Second, MaxInflight: 1}
	if p.Total() != 6 || p.WarmupShots() != 0 {
		t.Fatalf("total %d warmup %d, want 6 and 0", p.Total(), p.WarmupShots())
	}
	if got := p.Offset(1); got != 333333333*time.Nanosecond {
		t.Fatalf("Offset(1) = %v", got)
	}
	if got := p.Offset(3); got != time.Second {
		t.Fatalf("Offset(3) = %v, want exactly 1s", got)
	}
	odd := Plan{Rate: 7, Warmup: 500 * time.Millisecond, Duration: time.Second, MaxInflight: 1}
	if odd.Total() != 10 || odd.WarmupShots() != 3 {
		t.Fatalf("total %d warmup %d, want 10 and 3", odd.Total(), odd.WarmupShots())
	}
}

func TestRunShedsInsteadOfQueueingWhenTheInflightBoundIsFull(t *testing.T) {
	for _, tc := range []struct {
		inflight, shed int
	}{{5, 0}, {4, 20}} {
		synctest.Test(t, func(t *testing.T) {
			p := Plan{Rate: 1000, Duration: 100 * time.Millisecond, MaxInflight: tc.inflight}
			rec := &recorder{holdup: 4500 * time.Microsecond}
			c := Run(t.Context(), p, rec.fire)
			want := Window{Due: 100, Issued: 100 - tc.shed, Shed: tc.shed}
			if c.All != want || c.Measured != want {
				t.Fatalf("bound %d: counts = %+v, want %+v", tc.inflight, c, want)
			}
			if c.Lag.Max != 0 || c.Elapsed != 99*time.Millisecond {
				t.Fatalf("bound %d: pacer lagged %v and ended at %v, want it never held back", tc.inflight, c.Lag, c.Elapsed)
			}
			if rec.peak > tc.inflight {
				t.Fatalf("bound %d: %d sends in flight", tc.inflight, rec.peak)
			}
			for _, f := range rec.fired {
				if tc.shed > 0 && f.shot.Index%5 == 4 {
					t.Fatalf("shot %d was issued while the bound was full", f.shot.Index)
				}
			}
		})
	}
}

func TestRunCatchesUpOverdueShotsWithinTheInflightBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := Plan{Rate: 1000, Duration: 100 * time.Millisecond, MaxInflight: 8}
		rec := &recorder{holdup: 500 * time.Microsecond}
		start := time.Now().Add(-50 * time.Millisecond)
		c := run(t.Context(), p, start, rec.fire)
		want := Window{Due: 100, Issued: 57, Shed: 43}
		if c.All != want || c.Lag.Max != 50*time.Millisecond || c.Lag.P50 != 0 {
			t.Fatalf("counts = %+v, want %+v with 50ms max lag and most shots on time", c, want)
		}
		if rec.peak != 8 {
			t.Fatalf("peak in flight %d, want the catch-up burst capped at 8", rec.peak)
		}
		for _, f := range rec.fired {
			late := f.at.Sub(f.shot.Scheduled)
			switch {
			case f.shot.Index < 8 && late != time.Duration(50-f.shot.Index)*time.Millisecond:
				t.Fatalf("overdue shot %d fired %v late, want it issued at once", f.shot.Index, late)
			case f.shot.Index >= 8 && f.shot.Index <= 50:
				t.Fatalf("shot %d was issued while the catch-up burst filled the bound", f.shot.Index)
			case f.shot.Index > 50 && late != 0:
				t.Fatalf("shot %d fired %v late after the catch-up", f.shot.Index, late)
			}
		}
	})
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 50500*time.Microsecond)
		defer cancel()
		rec := &recorder{holdup: time.Hour}
		c := Run(ctx, Plan{Rate: 1000, Duration: time.Second, MaxInflight: 1000}, rec.fire)
		if c.All != (Window{Due: 51, Issued: 51}) {
			t.Fatalf("counts = %+v, want the 51 shots due before the cancel", c)
		}
	})
}

func TestValidateRejectsUnusablePlans(t *testing.T) {
	good := Plan{Rate: 10, Warmup: time.Second, Duration: time.Second, MaxInflight: 1}
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate(%+v) = %v", good, err)
	}
	for _, p := range []Plan{
		{Rate: 0, Duration: time.Second, MaxInflight: 1},
		{Rate: 10, Duration: 0, MaxInflight: 1},
		{Rate: 10, Duration: time.Second, MaxInflight: 0},
		{Rate: 10, Warmup: -time.Second, Duration: time.Second, MaxInflight: 1},
		{Rate: 10_000_000, Duration: time.Second, MaxInflight: 1},
		{Rate: 10, Duration: 48 * time.Hour, MaxInflight: 1},
		{Rate: 1, Duration: 500 * time.Millisecond, MaxInflight: 1},
	} {
		if err := p.Validate(); err == nil {
			t.Fatalf("Validate(%+v) accepted it", p)
		}
	}
}
