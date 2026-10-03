package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

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
	w := fs.String("w", "majority", "write concern: majority or 1")
	journal := fs.Bool("j", false, "also wait for the on-disk journal")
	_ = fs.Parse(args)
	wc, err := writeConcern(*w, *journal)
	if err != nil {
		return err
	}
	cfg.Rooms = make([]uint64, max(*rooms, 0))
	for i := range cfg.Rooms {
		cfg.Rooms[i] = ids.NewRoomID()
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	client, coll, err := t.connect(ctx, wc)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if err := createClustered(ctx, coll.Database(), t.coll); err != nil {
		return err
	}
	res := writeload.Run(ctx, cfg, func(ctx context.Context, batch []writeload.Msg) error {
		docs := make([]any, len(batch))
		for i, m := range batch {
			docs[i] = newMessage(m.Room, m.Seq, m.From, m.Text)
		}
		_, err := coll.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
		return err
	})
	res.Report(os.Stdout, fmt.Sprintf("w=%s j=%t", *w, *journal), cfg.Rate)
	return nil
}

func writeConcern(w string, journal bool) (*writeconcern.WriteConcern, error) {
	var wc *writeconcern.WriteConcern
	switch w {
	case "majority":
		wc = writeconcern.Majority()
	case "1":
		wc = writeconcern.W1()
	default:
		return nil, fmt.Errorf("invalid -w %q: use majority or 1", w)
	}
	if journal {
		wc.Journal = &journal
	}
	return wc, nil
}
