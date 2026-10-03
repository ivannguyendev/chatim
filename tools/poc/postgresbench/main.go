package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
		fmt.Fprintln(os.Stderr, "postgresbench:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: postgresbench seed|read|write [flags]  (use -h after a subcommand)")
	os.Exit(2)
}

type target struct{ uri, table string }

func (t *target) register(fs *flag.FlagSet) {
	fs.StringVar(&t.uri, "uri", cmp.Or(os.Getenv("PG_URI"), "postgres://chatim@chatim-postgres:5432/chatim_poc"), "PostgreSQL URI (env PG_URI)")
	fs.StringVar(&t.table, "table", "messages", "table")
}

func (t *target) ident() string { return pgx.Identifier{t.table}.Sanitize() }

func (t *target) connect(ctx context.Context, syncCommit string, maxConns int) (*pgxpool.Pool, error) {
	if syncCommit != "on" && syncCommit != "off" {
		return nil, fmt.Errorf("invalid -sync %q: use on or off", syncCommit)
	}
	cfg, err := pgxpool.ParseConfig(t.uri)
	if err != nil {
		return nil, fmt.Errorf("parse uri: %w", err)
	}
	cfg.MaxConns = int32(max(maxConns, 1))
	cfg.ConnConfig.RuntimeParams["synchronous_commit"] = syncCommit
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
