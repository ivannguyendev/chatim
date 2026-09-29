package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

type pending struct {
	doc message
	at  time.Time
}

type writeStats struct {
	msgLat, insLat         latency.Recorder
	batches, docs, errored atomic.Int64
}

func runWrite(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("write", flag.ExitOnError)
	var t target
	t.register(fs)
	rate := fs.Int("rate", 10_000, "messages per second")
	duration := fs.Duration("duration", 60*time.Second, "test length")
	rooms := fs.Int("rooms", 5000, "active rooms receiving messages")
	window := fs.Duration("window", 2*time.Millisecond, "flush window")
	maxBatch := fs.Int("max-batch", 256, "flush when a batch reaches this size")
	flushers := fs.Int("flushers", 6, "parallel flushers (≈ cores × flush workers)")
	_ = fs.Parse(args)

	client, coll, err := t.connect(ctx, writeconcern.Majority())
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if err := createClustered(ctx, coll.Database(), t.coll); err != nil {
		return err
	}
	roomIDs := make([]uint64, *rooms)
	for i := range roomIDs {
		roomIDs[i] = ids.NewRoomID()
	}

	in := make(chan pending, 100_000)
	var st writeStats
	start := time.Now()
	go generate(ctx, in, *rate, *duration, roomIDs)
	var wg sync.WaitGroup
	for i := 0; i < *flushers; i++ {
		wg.Go(func() { flushLoop(ctx, coll, in, *window, *maxBatch, &st) })
	}
	wg.Wait()
	elapsed := time.Since(start)
	fmt.Printf("target=%d/s achieved=%.0f/s docs=%d batches=%d avg-batch=%.1f errors=%d\n",
		*rate, float64(st.docs.Load())/elapsed.Seconds(), st.docs.Load(), st.batches.Load(),
		float64(st.docs.Load())/float64(max(st.batches.Load(), 1)), st.errored.Load())
	fmt.Printf("arrival->commit (what a sender waits for ack): %v\n", st.msgLat.Summary())
	fmt.Printf("insertMany w:majority round trip:              %v\n", st.insLat.Summary())
	return nil
}

func generate(ctx context.Context, out chan<- pending, rate int, d time.Duration, rooms []uint64) {
	defer close(out)
	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	seqs := make(map[uint64]uint64, len(rooms))
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	start, emitted := time.Now(), 0
	for range tick.C {
		elapsed := time.Since(start)
		if elapsed >= d || ctx.Err() != nil {
			return
		}
		for due := int(float64(rate) * elapsed.Seconds()); emitted < due; emitted++ {
			room := rooms[rng.IntN(len(rooms))]
			seqs[room]++
			select {
			case out <- pending{doc: newMessage(room, seqs[room], rng, nil), at: time.Now()}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func flushLoop(ctx context.Context, coll *mongo.Collection, in <-chan pending, window time.Duration, maxBatch int, st *writeStats) {
	batch := make([]pending, 0, maxBatch)
	timer := time.NewTimer(window)
	timer.Stop()
	flush := func() {
		timer.Stop()
		if len(batch) == 0 {
			return
		}
		docs := make([]any, len(batch))
		for i, p := range batch {
			docs[i] = p.doc
		}
		begin := time.Now()
		_, err := coll.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
		done := time.Now()
		if err != nil {
			st.errored.Add(int64(len(batch)))
			batch = batch[:0]
			return
		}
		st.insLat.Add(done.Sub(begin))
		for _, p := range batch {
			st.msgLat.Add(done.Sub(p.at))
		}
		st.batches.Add(1)
		st.docs.Add(int64(len(batch)))
		batch = batch[:0]
	}
	for {
		select {
		case p, ok := <-in:
			if !ok {
				flush()
				return
			}
			if len(batch) == 0 {
				timer.Reset(window)
			}
			if batch = append(batch, p); len(batch) >= maxBatch {
				flush()
			}
		case <-timer.C:
			flush()
		case <-ctx.Done():
			return
		}
	}
}
