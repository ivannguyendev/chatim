package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/identity/internal/config"
	"github.com/ivannguyendev/chatim/apps/identity/internal/signing"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

const usage = "usage: identity [serve|probe|<admin command> [flags]]"

func Main(args []string) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch {
	case admincmd.Is(cmd):
		return adminMain(args)
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
		slog.New(base).ErrorContext(ctx, "invalid identity config", "err", err)
		return 1
	}
	log := redactingLogger(base, cfg)
	slog.SetDefault(log)
	if err := Run(ctx, cfg, log, Deps{}); err != nil {
		log.ErrorContext(ctx, "identity exited with error", "err", err)
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
	return slog.New(redact.NewHandler(h, []string{cfg.MongoURI}, []string{cfg.MongoPassword}))
}

func adminMain(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	base := slog.NewJSONHandler(os.Stderr, nil)
	cfg, err := config.Load()
	if err != nil {
		slog.New(base).ErrorContext(ctx, "invalid identity config", "err", err)
		return 1
	}
	log := redactingLogger(base, cfg)
	sealer, err := signing.NewSealer(cfg.KeyEncryptionKey, rand.Reader)
	if err != nil {
		log.ErrorContext(ctx, "key encryption key", "err", err)
		return 1
	}
	env := admincmd.Env{
		Open: func(context.Context) (admincmd.Store, error) {
			st, err := mongostore.Open(mongoOptions(cfg, adminAppName))
			if err != nil {
				return nil, err
			}
			return st, nil
		},
		Sealer: sealer, Random: rand.Reader, Now: time.Now,
		KeyWindow: cfg.TokenTTL + tokenLeeway, ActivateLead: cfg.KeyActivateLead, Issuer: cfg.Issuer, Out: os.Stdout, Log: log,
	}
	return admincmd.Run(ctx, env, args)
}
