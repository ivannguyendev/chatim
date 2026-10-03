package writeload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
	"github.com/ivannguyendev/chatim/tools/poc/internal/msgtext"
)

type Msg struct {
	Room, Seq uint64
	From      string
	Text      string
	Arrived   time.Time
}

type Config struct {
	Rate     int
	Duration time.Duration
	Window   time.Duration
	MaxBatch int
	Flushers int
	Rooms    []uint64
}

type Result struct {
	Elapsed               time.Duration
	Docs, Batches, Failed int64
	Ack, Insert           latency.Summary
}

type Insert func(ctx context.Context, batch []Msg) error

func (c Config) Validate() error {
	if c.Rate <= 0 || c.Duration <= 0 || c.Window <= 0 || c.MaxBatch <= 0 || c.Flushers <= 0 || len(c.Rooms) == 0 {
		return errors.New("writeload: rate, duration, window, max-batch, flushers and rooms must be positive")
	}
	return nil
}

func Run(ctx context.Context, cfg Config, insert Insert) Result {
	var ack, ins latency.Recorder
	var docs, batches, failed atomic.Int64
	in := make(chan Msg, 100_000)
	start := time.Now()
	go generate(ctx, in, cfg)
	var wg sync.WaitGroup
	for range cfg.Flushers {
		wg.Go(func() {
			flushLoop(ctx, in, cfg, func(batch []Msg) {
				begin := time.Now()
				err := insert(ctx, slices.Clone(batch))
				done := time.Now()
				if err != nil {
					failed.Add(int64(len(batch)))
					return
				}
				ins.Add(done.Sub(begin))
				for _, m := range batch {
					ack.Add(done.Sub(m.Arrived))
				}
				batches.Add(1)
				docs.Add(int64(len(batch)))
			})
		})
	}
	wg.Wait()
	return Result{Elapsed: time.Since(start), Docs: docs.Load(), Batches: batches.Load(), Failed: failed.Load(), Ack: ack.Summary(), Insert: ins.Summary()}
}

func (r Result) Report(w io.Writer, label string, target int) {
	fmt.Fprintf(w, "%s target=%d/s achieved=%.0f/s docs=%d batches=%d avg-batch=%.1f errors=%d\n",
		label, target, float64(r.Docs)/r.Elapsed.Seconds(), r.Docs, r.Batches, float64(r.Docs)/float64(max(r.Batches, 1)), r.Failed)
	fmt.Fprintf(w, "arrival->commit (what a sender waits for ack): %v\n", r.Ack)
	fmt.Fprintf(w, "insert round trip:                             %v\n", r.Insert)
}

func generate(ctx context.Context, out chan<- Msg, cfg Config) {
	defer close(out)
	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	seqs := make(map[uint64]uint64, len(cfg.Rooms))
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	start, emitted := time.Now(), 0
	for range tick.C {
		elapsed := time.Since(start)
		if elapsed >= cfg.Duration || ctx.Err() != nil {
			return
		}
		for due := int(float64(cfg.Rate) * elapsed.Seconds()); emitted < due; emitted++ {
			room := cfg.Rooms[rng.IntN(len(cfg.Rooms))]
			seqs[room]++
			m := Msg{Room: room, Seq: seqs[room], From: msgtext.Sender(rng), Text: msgtext.Synthetic(rng), Arrived: time.Now()}
			select {
			case out <- m:
			case <-ctx.Done():
				return
			}
		}
	}
}

func flushLoop(ctx context.Context, in <-chan Msg, cfg Config, flush func([]Msg)) {
	batch := make([]Msg, 0, cfg.MaxBatch)
	timer := time.NewTimer(cfg.Window)
	timer.Stop()
	send := func() {
		timer.Stop()
		if len(batch) > 0 {
			flush(batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case m, ok := <-in:
			if !ok {
				send()
				return
			}
			if len(batch) == 0 {
				timer.Reset(cfg.Window)
			}
			if batch = append(batch, m); len(batch) >= cfg.MaxBatch {
				send()
			}
		case <-timer.C:
			send()
		case <-ctx.Done():
			return
		}
	}
}
