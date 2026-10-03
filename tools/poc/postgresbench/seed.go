package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/tools/poc/internal/msgtext"
	"github.com/ivannguyendev/chatim/tools/poc/internal/roomset"
	"github.com/ivannguyendev/chatim/tools/poc/internal/seedload"
)

func runSeed(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	var t target
	t.register(fs)
	rooms := fs.Int("rooms", 10_000, "number of rooms")
	perRoom := fs.Int("per-room", 1000, "messages per room")
	workers := fs.Int("workers", 8, "parallel COPY workers")
	batch := fs.Int("batch", 1000, "rows per COPY")
	order := fs.String("order", "interleaved", "insert order: interleaved (rooms mixed like real traffic) or room (one room at a time)")
	partitions := fs.Int("partitions", 0, "hash partitions on room_id (0 = plain table)")
	reset := fs.Bool("reset", false, "drop the table first")
	roomsFile := fs.String("rooms-file", "rooms-pg.txt", "file that receives the seeded room ids")
	textFile := fs.String("text-file", "", "optional file of real message texts, one per line (needed for credible storage numbers)")
	_ = fs.Parse(args)
	texts, err := msgtext.Load(*textFile)
	if err != nil {
		return err
	}
	roomIDs := make([]uint64, *rooms)
	for i := range roomIDs {
		roomIDs[i] = ids.NewRoomID()
	}
	jobs, err := roomset.Jobs(*order, roomIDs, *perRoom, *batch)
	if err != nil {
		return err
	}

	pool, err := t.connect(ctx, "off", *workers+1)
	if err != nil {
		return err
	}
	defer pool.Close()
	if *reset {
		if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+t.ident()+" CASCADE"); err != nil {
			return fmt.Errorf("drop: %w", err)
		}
	}
	if err := createTable(ctx, pool, t.table, *partitions); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	if err := roomset.Write(*roomsFile, roomIDs, *perRoom); err != nil {
		return err
	}

	start := time.Now()
	n, err := seedload.Run(ctx, jobs, *workers, texts, func(ctx context.Context, rows []seedload.Row) error {
		now := time.Now()
		_, err := pool.CopyFrom(ctx, pgx.Identifier{t.table}, columns, pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			r := rows[i]
			return []any{int64(r.Room), int64(0), int64(r.Seq), r.From, int64(r.Seq), int16(1), r.Text, now}, nil
		}))
		return err
	})
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, "ANALYZE "+t.ident()); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	fmt.Printf("seeded %d messages (order=%s partitions=%d) in %v\n", n, *order, *partitions, time.Since(start).Round(time.Second))
	return printStorage(ctx, pool, t.table)
}
