package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/pkg/keys"
	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
	"github.com/ivannguyendev/chatim/tools/poc/internal/roomset"
)

func runRead(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	var t target
	t.register(fs)
	mode := fs.String("mode", "oldest", "page to read: oldest | random | latest")
	roomsFile := fs.String("rooms-file", "rooms-mongo.txt", "room ids written by seed")
	concurrency := fs.Int("concurrency", 16, "parallel readers")
	duration := fs.Duration("duration", 30*time.Second, "how long to read")
	limit := fs.Int64("limit", 50, "messages per page")
	_ = fs.Parse(args)
	switch *mode {
	case "oldest", "random", "latest":
	default:
		return fmt.Errorf("unknown -mode %q", *mode)
	}

	rooms, err := roomset.Read(*roomsFile)
	if err != nil {
		return err
	}
	client, coll, err := t.connect(ctx, writeconcern.W1())
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	filter, sort := pageQuery(*mode, rooms[0], rand.New(rand.NewPCG(1, 1)))
	stages, err := explain(ctx, coll, filter, sort, *limit)
	if err != nil {
		return err
	}
	fmt.Printf("plan (%s page): %s\n", *mode, strings.Join(stages, " > "))

	var rec latency.Recorder
	var pages, empty atomic.Int64
	var failed atomic.Int64
	var firstErr atomic.Value
	runCtx, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()
	var wg sync.WaitGroup
	for w := 0; w < *concurrency; w++ {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			for runCtx.Err() == nil {
				filter, sort := pageQuery(*mode, rooms[rng.IntN(len(rooms))], rng)
				start := time.Now()
				n, err := readPage(runCtx, coll, filter, sort, *limit)
				if err != nil {
					if runCtx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) && failed.Add(1) == 1 {
						firstErr.Store(err.Error())
					}
					continue
				}
				rec.Add(time.Since(start))
				pages.Add(1)
				if n == 0 {
					empty.Add(1)
				}
			}
		})
	}
	wg.Wait()
	fmt.Printf("mode=%s pages=%d (%.0f/s) empty=%d errors=%d latency: %v\n",
		*mode, pages.Load(), float64(pages.Load())/duration.Seconds(), empty.Load(), failed.Load(), rec.Summary())
	if msg, ok := firstErr.Load().(string); ok {
		fmt.Println("first error:", msg)
	}
	return nil
}

func pageQuery(mode string, r roomset.Room, rng *rand.Rand) (filter, sort bson.D) {
	upper, dir := uint64(math.MaxUint64), -1
	switch mode {
	case "oldest":
		dir = 1
	case "random":
		upper = 2 + rng.Uint64N(r.PerRoom)
	}
	lo, hi := keys.MsgRange(r.ID, 0, 0, upper)
	filter = bson.D{{Key: "_id", Value: bson.D{{Key: "$gte", Value: lo}, {Key: "$lt", Value: hi}}}}
	return filter, bson.D{{Key: "_id", Value: dir}}
}

func readPage(ctx context.Context, coll *mongo.Collection, filter, sort bson.D, limit int64) (int, error) {
	cur, err := coll.Find(ctx, filter, options.Find().SetSort(sort).SetLimit(limit))
	if err != nil {
		return 0, err
	}
	defer func() { _ = cur.Close(context.Background()) }()
	n := 0
	for cur.Next(ctx) {
		_ = cur.Current
		n++
	}
	return n, cur.Err()
}
