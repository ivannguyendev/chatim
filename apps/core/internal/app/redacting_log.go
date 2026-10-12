package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

type redactingHandler struct {
	inner   slog.Handler
	urls    []string
	secrets []string
}

func redactedLogger(log *slog.Logger, cfg config.Config) *slog.Logger {
	return slog.New(&redactingHandler{inner: log.Handler(), urls: []string{cfg.MongoURI, cfg.NATSURL}, secrets: cfg.Secrets()})
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, h.clean(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &redactingHandler{inner: h.inner.WithAttrs(h.attrs(attrs)), urls: h.urls, secrets: h.secrets}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: h.inner.WithGroup(name), urls: h.urls, secrets: h.secrets}
}

func (h *redactingHandler) clean(s string) string {
	return config.RedactSecrets(config.RedactText(s, h.urls...), h.secrets...)
}

func (h *redactingHandler) attrs(in []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(in))
	for i, a := range in {
		out[i] = h.attr(a)
	}
	return out
}

func (h *redactingHandler) attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.clean(v.String()))
	case slog.KindGroup:
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(h.attrs(v.Group())...)}
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return slog.String(a.Key, h.clean(err.Error()))
		}
		if s := fmt.Sprint(v.Any()); h.clean(s) != s {
			return slog.String(a.Key, h.clean(s))
		}
		return slog.Attr{Key: a.Key, Value: v}
	default:
		return slog.Attr{Key: a.Key, Value: v}
	}
}
