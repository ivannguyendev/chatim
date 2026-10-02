package openloop

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

const (
	maxRate = 1_000_000
	maxSpan = time.Hour
)

type Plan struct {
	Rate        int
	Warmup      time.Duration
	Duration    time.Duration
	MaxInflight int
}

type Shot struct {
	Index     int
	Scheduled time.Time
	Measured  bool
}

type Window struct {
	Due, Issued, Shed int
}

type Counts struct {
	All, Measured Window
	Lag           latency.Summary
	Elapsed       time.Duration
}

type Fire func(ctx context.Context, s Shot)

func (p Plan) Validate() error {
	switch {
	case p.Rate <= 0 || p.Rate > maxRate:
		return errors.New("openloop: rate must be in 1..1000000 per second")
	case p.Duration <= 0 || p.Warmup < 0 || p.Warmup+p.Duration > maxSpan:
		return errors.New("openloop: duration must be positive, warmup not negative, together at most 1h")
	case p.MaxInflight <= 0:
		return errors.New("openloop: max in-flight must be positive")
	case p.Total() == p.WarmupShots():
		return errors.New("openloop: the measured window schedules no shot")
	}
	return nil
}

func (p Plan) Total() int { return p.shotsWithin(p.Warmup + p.Duration) }

func (p Plan) WarmupShots() int { return p.shotsWithin(p.Warmup) }

func (p Plan) Offset(i int) time.Duration {
	return time.Duration(int64(i) * int64(time.Second) / int64(p.Rate))
}

func (p Plan) shotsWithin(d time.Duration) int {
	return int(int64(p.Rate) * int64(d) / int64(time.Second))
}

func Run(ctx context.Context, p Plan, fire Fire) Counts {
	return run(ctx, p, time.Now(), fire)
}

func run(ctx context.Context, p Plan, start time.Time, fire Fire) Counts {
	total, warm := p.Total(), p.WarmupShots()
	slots := make(chan struct{}, p.MaxInflight)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	var wg sync.WaitGroup
	var lag latency.Recorder
	var c Counts
	for i := range total {
		due := start.Add(p.Offset(i))
		if wait := time.Until(due); wait > 0 {
			timer.Reset(wait)
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			break
		}
		s := Shot{Index: i, Scheduled: due, Measured: i >= warm}
		if s.Measured {
			lag.Add(time.Since(due))
		}
		issued := false
		select {
		case slots <- struct{}{}:
			issued = true
			wg.Go(func() {
				defer func() { <-slots }()
				fire(ctx, s)
			})
		default:
		}
		c.All.add(issued)
		if s.Measured {
			c.Measured.add(issued)
		}
	}
	c.Elapsed = time.Since(start)
	c.Lag = lag.Summary()
	wg.Wait()
	return c
}

func (w *Window) add(issued bool) {
	w.Due++
	if issued {
		w.Issued++
	} else {
		w.Shed++
	}
}
