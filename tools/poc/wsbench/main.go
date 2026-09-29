package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	run := map[string]func(context.Context, []string) error{"server": runServer, "client": runClient}[os.Args[1]]
	if run == nil {
		usage()
	}
	if err := run(ctx, os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "wsbench:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: wsbench server|client [flags]  (use -h after a subcommand)")
	os.Exit(2)
}
