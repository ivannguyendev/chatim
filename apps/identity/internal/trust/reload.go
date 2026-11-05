package trust

import (
	"context"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

type Reloader struct {
	OnReady func()

	verifier *Verifier
	source   store.TrustSource
	every    time.Duration
	log      *slog.Logger
	wake     chan struct{}
	readied  bool
}

func NewReloader(v *Verifier, src store.TrustSource, every time.Duration, log *slog.Logger) *Reloader {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Reloader{verifier: v, source: src, every: every, log: log, wake: make(chan struct{}, 1)}
}

func (r *Reloader) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Reloader) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.every)
	defer ticker.Stop()
	for {
		r.load(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-r.wake:
		}
	}
}

func (r *Reloader) load(ctx context.Context) {
	loadCtx, cancel := context.WithTimeout(ctx, r.every)
	defer cancel()
	res, err := r.verifier.Load(loadCtx, r.source)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		r.log.WarnContext(ctx, "trust reload failed, keeping the last snapshot", slog.String("error", err.Error()))
		return
	}
	for _, k := range res.Skipped {
		r.log.WarnContext(ctx, "trusted key skipped", slog.String("issuer", k.Issuer), slog.String("kid", k.KeyID))
	}
	if !r.readied {
		r.readied = true
		if r.OnReady != nil {
			r.OnReady()
		}
	}
}
