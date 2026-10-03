package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/tools/poc/internal/writeload"
)

func runWrite(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("write", flag.ExitOnError)
	var t target
	t.register(fs)
	cfg := writeload.Config{}
	fs.IntVar(&cfg.Rate, "rate", 10_000, "messages per second")
	fs.DurationVar(&cfg.Duration, "duration", 60*time.Second, "test length")
	rooms := fs.Int("rooms", 5000, "active rooms receiving messages")
	fs.DurationVar(&cfg.Window, "window", 2*time.Millisecond, "flush window")
	fs.IntVar(&cfg.MaxBatch, "max-batch", 256, "flush when a batch reaches this size")
	fs.IntVar(&cfg.Flushers, "flushers", 6, "parallel flushers (≈ cores × flush workers)")
	syncCommit := fs.String("sync", "on", "synchronous_commit: on (wait for WAL flush) or off")
	_ = fs.Parse(args)
	cfg.Rooms = make([]uint64, max(*rooms, 0))
	for i := range cfg.Rooms {
		cfg.Rooms[i] = ids.NewRoomID()
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	pool, err := t.connect(ctx, *syncCommit, cfg.Flushers+1)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := createTable(ctx, pool, t.table, 0); err != nil {
		return err
	}
	insert := "INSERT INTO " + t.ident() + " (" + strings.Join(columns, ", ") + ") " +
		"SELECT * FROM unnest($1::bigint[], $2::bigint[], $3::bigint[], $4::text[], $5::bigint[], $6::smallint[], $7::text[], $8::timestamptz[]) " +
		"ON CONFLICT DO NOTHING"
	res := writeload.Run(ctx, cfg, func(ctx context.Context, batch []writeload.Msg) error {
		n := len(batch)
		roomIDs, threads, seqs, froms := make([]int64, n), make([]int64, n), make([]int64, n), make([]string, n)
		pts, kinds, bodies, stamps := make([]int64, n), make([]int16, n), make([]string, n), make([]time.Time, n)
		now := time.Now()
		for i, m := range batch {
			roomIDs[i], seqs[i], froms[i], pts[i], kinds[i], bodies[i], stamps[i] = int64(m.Room), int64(m.Seq), m.From, int64(m.Seq), 1, m.Text, now
		}
		_, err := pool.Exec(ctx, insert, roomIDs, threads, seqs, froms, pts, kinds, bodies, stamps)
		return err
	})
	res.Report(os.Stdout, "sync="+*syncCommit, cfg.Rate)
	return nil
}
