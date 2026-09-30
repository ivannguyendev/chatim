package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

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
	workers := fs.Int("workers", 8, "parallel insert workers")
	batch := fs.Int("batch", 1000, "documents per insertMany")
	order := fs.String("order", "interleaved", "insert order: interleaved (rooms mixed like real traffic) or room (one room at a time)")
	reset := fs.Bool("reset", false, "drop the collection first")
	roomsFile := fs.String("rooms-file", "rooms-mongo.txt", "file that receives the seeded room ids")
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

	client, coll, err := t.connect(ctx, writeconcern.W1())
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if *reset {
		if err := coll.Drop(ctx); err != nil {
			return fmt.Errorf("drop: %w", err)
		}
	}
	if err := createClustered(ctx, coll.Database(), t.coll); err != nil {
		return err
	}
	if err := roomset.Write(*roomsFile, roomIDs, *perRoom); err != nil {
		return err
	}

	start := time.Now()
	n, err := seedload.Run(ctx, jobs, *workers, texts, func(ctx context.Context, rows []seedload.Row) error {
		docs := make([]any, len(rows))
		for i, r := range rows {
			docs[i] = newMessage(r.Room, r.Seq, r.From, r.Text)
		}
		_, err := coll.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
		return err
	})
	if err != nil {
		return err
	}
	fmt.Printf("seeded %d messages (order=%s) in %v\n", n, *order, time.Since(start).Round(time.Second))
	return printStorage(ctx, coll)
}

func createClustered(ctx context.Context, db *mongo.Database, name string) error {
	names, err := db.ListCollectionNames(ctx, bson.D{{Key: "name", Value: name}})
	if err != nil || len(names) > 0 {
		return err
	}
	opts := options.CreateCollection().
		SetClusteredIndex(bson.D{{Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "unique", Value: true}}).
		SetStorageEngine(bson.D{{Key: "wiredTiger", Value: bson.D{{Key: "configString", Value: "block_compressor=zstd"}}}})
	return db.CreateCollection(ctx, name, opts)
}
