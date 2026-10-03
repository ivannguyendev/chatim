package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
	"github.com/ivannguyendev/chatim/tools/poc/internal/roomset"
)

func runRead(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	var t target
	t.register(fs)
	mode := fs.String("mode", "oldest", "page to read: oldest | random | latest")
	roomsFile := fs.String("rooms-file", "rooms-pg.txt", "room ids written by seed")
	concurrency := fs.Int("concurrency", 16, "parallel readers")
	duration := fs.Duration("duration", 30*time.Second, "how long to read")
	limit := fs.Int64("limit", 50, "messages per page")
	_ = fs.Parse(args)
	sql, err := pageSQL(*mode, t.ident())
	if err != nil {
		return err
	}
	rooms, err := roomset.Read(*roomsFile)
	if err != nil {
		return err
	}
	pool, err := t.connect(ctx, "on", *concurrency+1)
	if err != nil {
		return err
	}
	defer pool.Close()

	stages, err := explain(ctx, pool, sql, pageArgs(*mode, rooms[0], *limit, rand.New(rand.NewPCG(1, 1)))...)
	if err != nil {
		return err
	}
	fmt.Printf("plan (%s page): %s\n", *mode, strings.Join(stages, " > "))

	var rec latency.Recorder
	var pages, empty, failed atomic.Int64
	var firstErr atomic.Value
	runCtx, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()
	var wg sync.WaitGroup
	for w := 0; w < *concurrency; w++ {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			for runCtx.Err() == nil {
				room := rooms[rng.IntN(len(rooms))]
				start := time.Now()
				n, err := readPage(runCtx, pool, sql, pageArgs(*mode, room, *limit, rng))
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

func pageSQL(mode, table string) (string, error) {
	base := "SELECT room_id, thread_root, seq, f, p, kind, body, ts FROM " + table + " WHERE room_id = $1 AND thread_root = 0"
	switch mode {
	case "oldest":
		return base + " ORDER BY seq ASC LIMIT $2", nil
	case "latest":
		return base + " ORDER BY seq DESC LIMIT $2", nil
	case "random":
		return base + " AND seq < $3 ORDER BY seq DESC LIMIT $2", nil
	}
	return "", fmt.Errorf("unknown -mode %q", mode)
}

func pageArgs(mode string, r roomset.Room, limit int64, rng *rand.Rand) []any {
	args := []any{int64(r.ID), limit}
	if mode == "random" {
		args = append(args, int64(2+rng.Uint64N(r.PerRoom)))
	}
	return args
}

func readPage(ctx context.Context, pool *pgxpool.Pool, sql string, args []any) (int, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		_ = rows.RawValues()
		n++
	}
	return n, rows.Err()
}
