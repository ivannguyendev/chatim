package main

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/pkg/ids"
)

type opcounters struct {
	Insert  int64 `bson:"insert"`
	Query   int64 `bson:"query"`
	Update  int64 `bson:"update"`
	Delete  int64 `bson:"delete"`
	GetMore int64 `bson:"getmore"`
	Command int64 `bson:"command"`
	Conns   int64 `bson:"-"`
}

func (o opcounters) minus(b opcounters) opcounters {
	return opcounters{o.Insert - b.Insert, o.Query - b.Query, o.Update - b.Update, o.Delete - b.Delete, o.GetMore - b.GetMore, o.Command - b.Command, o.Conns - b.Conns}
}

func (o opcounters) reads() int64 { return o.Query + o.Command - 1 }

func (o opcounters) all() int64 {
	return o.Insert + o.Query + o.Update + o.Delete + o.GetMore + o.Command - 1
}

type mongoProbe struct {
	client *mongo.Client
	db     string
	rooms  *mongo.Collection
}

func openProbe(ctx context.Context, uri, db string) (*mongoProbe, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMaxPoolSize(2).SetAppName("chatim-roomlistbench"))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	return &mongoProbe{client: client, db: db, rooms: client.Database(db).Collection("rooms")}, nil
}

func (p *mongoProbe) close() { _ = p.client.Disconnect(context.Background()) }

func (p *mongoProbe) counters(ctx context.Context) (opcounters, error) {
	cmd := bson.D{{Key: "serverStatus", Value: 1}, {Key: "repl", Value: 0}, {Key: "metrics", Value: 0}, {Key: "locks", Value: 0}, {Key: "wiredTiger", Value: 0}, {Key: "tcmalloc", Value: 0}}
	var out struct {
		Ops   opcounters `bson:"opcounters"`
		Conns struct {
			TotalCreated int64 `bson:"totalCreated"`
		} `bson:"connections"`
	}
	if err := p.client.Database("admin").RunCommand(ctx, cmd).Decode(&out); err != nil {
		return opcounters{}, fmt.Errorf("serverStatus: %w", err)
	}
	out.Ops.Conns = out.Conns.TotalCreated
	return out.Ops, nil
}

func (p *mongoProbe) awaitQuiet(ctx context.Context, maxOpsPerSec int64, timeout time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	quiet := 0
	for {
		before, err := p.counters(ctx)
		if err != nil {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("mongo did not go quiet below %d ops/s: %w", maxOpsPerSec, ctx.Err())
		case <-time.After(time.Second):
		}
		after, err := p.counters(ctx)
		if err != nil {
			return 0, err
		}
		rate := after.minus(before).all()
		quiet++
		if rate > maxOpsPerSec {
			quiet = 0
		}
		if quiet == 3 {
			return rate, nil
		}
	}
}

func (p *mongoProbe) awaitActivity(ctx context.Context, rooms []string, seq int64, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	keys := make(bson.A, 0, len(rooms))
	for _, r := range rooms {
		id, err := ids.ParseRoomID(r)
		if err != nil {
			return err
		}
		keys = append(keys, int64(id))
	}
	for {
		var behind int64
		for from := 0; from < len(keys); from += 1000 {
			filter := bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: keys[from:min(from+1000, len(keys))]}}}, {Key: "$or", Value: bson.A{
				bson.D{{Key: "last_seq", Value: bson.D{{Key: "$exists", Value: false}}}},
				bson.D{{Key: "last_seq", Value: bson.D{{Key: "$lt", Value: seq}}}},
			}}}
			n, err := p.rooms.CountDocuments(ctx, filter)
			if err != nil {
				return fmt.Errorf("count rooms behind: %w", err)
			}
			behind += n
		}
		if behind == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%d rooms still behind seq %d: %w", behind, seq, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}
