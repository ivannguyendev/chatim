package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func resyncMain(args []string) int {
	opts, err := resync.ParseArgs(args, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.ErrorContext(ctx, "invalid core config", "err", err)
		return 1
	}
	log := redactedLogger(logger, cfg)
	if err := RunResync(ctx, cfg, opts, log, os.Stdout); err != nil {
		log.ErrorContext(ctx, "resync failed", "err", err)
		return 1
	}
	return 0
}

func RunResync(ctx context.Context, cfg config.Config, opts resync.Options, log *slog.Logger, out io.Writer) error {
	c := &clients{}
	defer c.close(ctx, log)
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	err := c.connectMongo(connectCtx, cfg)
	cancel()
	if err == nil {
		err = c.connectNATS(cfg, log)
	}
	if err != nil {
		return err
	}
	st := mongostore.New(c.mongo.Database(cfg.MongoDB), mongostore.Options{})
	target := resync.Target{SubjectRoot: cfg.Work.SubjectRoot, Partitions: cfg.Work.Partitions}
	deps := resync.Deps{Rooms: st, Pages: st, Edits: st, Reactions: st.Reactions(), Pins: st.Pins(), Members: st, Hidden: st.Hidden(), Pub: c.js}
	rep, err := resync.Run(ctx, deps, target, opts)
	fmt.Fprintln(out, rep)
	return err
}
