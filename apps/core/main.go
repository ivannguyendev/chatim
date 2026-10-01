package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

const usage = "usage: core [serve|probe]"

func main() { os.Exit(realMain(os.Args[1:])) }

func realMain(args []string) int {
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
		logger.ErrorContext(ctx, "invalid core config", "err", err)
		return 1
	}
	if err := run(ctx, cfg, logger); err != nil {
		logger.ErrorContext(ctx, "core exited with error", "err", err)
		return 1
	}
	return 0
}

func probeMain() int {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	if err := probe(ctx, config.AdminAddr()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
