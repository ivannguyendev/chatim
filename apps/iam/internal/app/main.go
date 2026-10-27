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

	"github.com/ivannguyendev/chatim/apps/iam/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/iam/internal/config"
	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

const adminPoolConns = 2

const usage = "usage: iam [serve|probe|<admin command> [flags]]"

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

func adminMain(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	base := slog.NewJSONHandler(os.Stderr, nil)
	cfg, err := config.Load()
	if err != nil {
		slog.New(base).ErrorContext(ctx, "invalid iam config", "err", err)
		return 1
	}
	log := redactingLogger(base, cfg)
	sealer, err := signing.NewSealer(cfg.KeyEncryptionKey, rand.Reader)
	if err != nil {
		log.ErrorContext(ctx, "key encryption key", "err", err)
		return 1
	}
	hasher, err := password.NewHasher(password.DefaultParams, rand.Reader)
	if err != nil {
		log.ErrorContext(ctx, "password hasher", "err", err)
		return 1
	}
	env := admincmd.Env{
		Open: func(ctx context.Context) (admincmd.Store, error) {
			return pgstore.Open(ctx, cfg.DatabaseURI(), pgstore.Options{MaxConns: adminPoolConns, RevocationKeep: cfg.RevocationKeep()})
		},
		Sealer: sealer, Hasher: hasher, Random: rand.Reader, Now: time.Now,
		KeyWindow: cfg.AccessTTL + tokenLeeway, Out: os.Stdout, Log: log,
	}
	return admincmd.Run(ctx, env, args)
}
