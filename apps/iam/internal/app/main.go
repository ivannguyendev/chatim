package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ivannguyendev/chatim/apps/iam/internal/config"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

const usage = "usage: iam [serve|probe]"

func Main(args []string) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch {
	case len(args) > 1:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	case cmd == "serve":
		return serveMain()
	case cmd == "probe":
		return probeMain()
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
}

func serveMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop)
	base := slog.NewJSONHandler(os.Stdout, nil)
	cfg, err := config.Load()
	if err != nil {
		slog.New(base).ErrorContext(ctx, "invalid iam config", "err", err)
		return 1
	}
	log := redactingLogger(base, cfg)
	slog.SetDefault(log)
	if err := Run(ctx, cfg, log, Deps{}); err != nil {
		log.ErrorContext(ctx, "iam exited with error", "err", err)
		return 1
	}
	return 0
}

func probeMain() int {
	ctx, cancel := context.WithTimeout(context.Background(), admin.ProbeTimeout)
	defer cancel()
	if err := admin.Probe(ctx, config.AdminAddr()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func redactingLogger(h slog.Handler, cfg config.Config) *slog.Logger {
	return slog.New(redact.NewHandler(h, []string{cfg.DatabaseURI()}, []string{cfg.PGPassword}))
}
