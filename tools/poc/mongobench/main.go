package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	run := map[string]func(context.Context, []string) error{"seed": runSeed, "read": runRead, "write": runWrite}[os.Args[1]]
	if run == nil {
		usage()
	}
	if err := run(ctx, os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "mongobench:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: mongobench seed|read|write [flags]  (use -h after a subcommand)")
	os.Exit(2)
}

type target struct{ uri, db, coll string }

func (t *target) register(fs *flag.FlagSet) {
	fs.StringVar(&t.uri, "uri", cmp.Or(os.Getenv("MONGO_URI"), "mongodb://chatim-mongodb:27017/?replicaSet=rs0&authSource=admin"), "MongoDB URI (env MONGO_URI)")
	fs.StringVar(&t.db, "db", "chatim_poc", "database")
	fs.StringVar(&t.coll, "coll", "messages", "collection")
}

func (t *target) connect(ctx context.Context, wc *writeconcern.WriteConcern) (*mongo.Client, *mongo.Collection, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(t.uri))
	if err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(ctx)
		return nil, nil, fmt.Errorf("ping: %w", err)
	}
	coll := client.Database(t.db).Collection(t.coll, options.Collection().SetWriteConcern(wc))
	return client, coll, nil
}
