package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/pkg/ids"
)

func runSeed(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	var t target
	t.register(fs)
	rooms := fs.Int("rooms", 10_000, "number of rooms")
	perRoom := fs.Int("per-room", 1000, "messages per room")
	workers := fs.Int("workers", 8, "parallel insert workers")
	batch := fs.Int("batch", 1000, "documents per insertMany")
	reset := fs.Bool("reset", false, "drop the collection first")
	roomsFile := fs.String("rooms-file", "rooms.txt", "file that receives the seeded room ids")
	textFile := fs.String("text-file", "", "optional file of real message texts, one per line (needed for credible storage numbers)")
	_ = fs.Parse(args)
	texts, err := loadTexts(*textFile)
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
	roomIDs := make([]uint64, *rooms)
	for i := range roomIDs {
		roomIDs[i] = ids.NewRoomID()
	}
	if err := writeRooms(*roomsFile, roomIDs, *perRoom); err != nil {
		return err
	}

	start := time.Now()
	var inserted atomic.Int64
	stopProgress := progress(&inserted, int64(*rooms)*int64(*perRoom))
	jobs := make(chan uint64)
	errs := make(chan error, *workers)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			for room := range jobs {
				for from := 1; from <= *perRoom; from += *batch {
					docs := make([]any, 0, *batch)
					for seq := from; seq < from+*batch && seq <= *perRoom; seq++ {
						docs = append(docs, newMessage(room, uint64(seq), rng, texts))
					}
					if _, err := coll.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false)); err != nil {
						errs <- fmt.Errorf("insert room %d: %w", room, err)
						return
					}
					inserted.Add(int64(len(docs)))
				}
			}
		})
	}
feed:
	for _, room := range roomIDs {
		select {
		case jobs <- room:
		case err := <-errs:
			close(jobs)
			wg.Wait()
			return err
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	stopProgress()
	select {
	case err := <-errs:
		return err
	default:
	}
	fmt.Printf("seeded %d messages in %v\n", inserted.Load(), time.Since(start).Round(time.Second))
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

func writeRooms(path string, roomIDs []uint64, perRoom int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, id := range roomIDs {
		fmt.Fprintf(w, "%d %d\n", id, perRoom)
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func progress(done *atomic.Int64, total int64) (stop func()) {
	t := time.NewTicker(5 * time.Second)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-t.C:
				fmt.Printf("  %d / %d messages\n", done.Load(), total)
			case <-quit:
				return
			}
		}
	}()
	return func() { t.Stop(); close(quit) }
}
