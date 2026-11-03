package app

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/api"
	"github.com/ivannguyendev/chatim/apps/identity/internal/config"
	"github.com/ivannguyendev/chatim/apps/identity/internal/exchange"
	"github.com/ivannguyendev/chatim/apps/identity/internal/guard"
	"github.com/ivannguyendev/chatim/apps/identity/internal/keyring"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

type trustParts struct {
	verifier *trust.Verifier
	reloader *trust.Reloader
	service  *exchange.Service
	ips      *guard.IPLimiter
	apiStats *api.ExchangeStats
}

func newTrust(cfg config.Config, st Store, dir *directoryParts, ring *keyring.Ring, log *slog.Logger) (*trustParts, error) {
	verifier, err := trust.New(trust.Config{Audience: cfg.ExchangeAudience, MaxLifetime: cfg.ExchangeMaxLifetime, Leeway: tokenLeeway})
	if err != nil {
		return nil, err
	}
	reloader := trust.NewReloader(verifier, st, cfg.TrustReload, log)
	dir.mirror.OnTrust = reloader.Wake
	return &trustParts{
		verifier: verifier,
		reloader: reloader,
		service: exchange.New(exchange.Deps{
			Verifier: verifier, Directory: dir.cache, Profiles: st, Signers: ring, Random: rand.Reader, Now: time.Now, Log: log,
		}),
		ips:      guard.NewIPLimiter(float64(cfg.ExchangeIPRate), cfg.ExchangeIPBurst, cfg.ExchangeIPTracked),
		apiStats: &api.ExchangeStats{},
	}, nil
}

func (t *trustParts) run(ctx context.Context, onReady func()) error {
	t.reloader.OnReady = onReady
	if err := t.reloader.Run(ctx); !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func trustMetricSources(t *trustParts) []metrics.Source {
	calls, writes := t.apiStats, t.service.Stats()
	out := []metrics.Source{
		counter("trust_reload_errors_total", "Failed trusted issuer and key reloads, plus trusted keys skipped as unusable.", t.verifier.Stats().ReloadErrors),
		counter("store_errors_total", "MongoDB errors on the token exchange path (just-in-time profile writes).", writes.StoreErrors),
	}
	for _, s := range []struct {
		label string
		read  func() uint64
	}{
		{"ok", calls.OK.Load}, {"invalid", calls.Invalid.Load}, {"disabled", calls.Disabled.Load},
		{"rate", calls.Limited.Load}, {"error", calls.Errors.Load},
	} {
		out = append(out, labelledCounter("exchanges_total", "Token exchanges on POST /v1/token by result.", "result", s.label, s.read))
	}
	for _, r := range exchange.WriteResults {
		out = append(out, labelledCounter("jit_writes_total", "Just-in-time profile writes by the token exchange by result.", "result", r.String(),
			func() uint64 { return writes.Writes(r) }))
	}
	return out
}
