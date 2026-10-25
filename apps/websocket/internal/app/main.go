package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/config"
	"github.com/ivannguyendev/chatim/pkg/admin"
)

const usage = "usage: websocket [serve|probe]"

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

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.ErrorContext(ctx, "invalid websocket config", "err", err)
		return 1
	}
	log := redactedLogger(logger, cfg)
	slog.SetDefault(log)
	if err := Run(ctx, cfg, log, Deps{}); err != nil {
		log.ErrorContext(ctx, "websocket exited with error", "err", err)
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
