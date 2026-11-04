package redact

import (
	"context"
	"fmt"
	"log/slog"
)

type Handler struct {
	inner   slog.Handler
	urls    []string
	secrets []string
}

func NewHandler(inner slog.Handler, urls, secrets []string) *Handler {
	return &Handler{inner: inner, urls: urls, secrets: secrets}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, h.clean(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{inner: h.inner.WithAttrs(h.attrs(attrs)), urls: h.urls, secrets: h.secrets}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name), urls: h.urls, secrets: h.secrets}
}

func (h *Handler) clean(s string) string {
	return Secrets(Text(s, h.urls...), h.secrets...)
}

func (h *Handler) attrs(in []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(in))
	for i, a := range in {
		out[i] = h.attr(a)
	}
	return out
}

func (h *Handler) attr(a slog.Attr) slog.Attr {
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
