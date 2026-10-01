package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() { os.Exit(realMain()) }

func realMain() int {
	if len(os.Args) < 2 {
		usage()
	}
	run := map[string]func(context.Context, []string) error{"server": runServer, "client": runClient}[os.Args[1]]
	if run == nil {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "wsbench:", err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: wsbench server|client [flags]  (use -h after a subcommand)")
	os.Exit(2)
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

func requirePositive[T int | time.Duration](name string, v T) error {
	if v <= 0 {
		return fmt.Errorf("invalid -%s: %v (must be > 0)", name, v)
	}
	return nil
}
