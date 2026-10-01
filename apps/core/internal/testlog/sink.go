package testlog

import (
	"context"
	"log/slog"
	"sync"
)

type Sink struct {
	mu   sync.Mutex
	msgs []string
}

func (s *Sink) Logger() *slog.Logger { return slog.New(s) }

func (s *Sink) Enabled(context.Context, slog.Level) bool { return true }

func (s *Sink) Handle(_ context.Context, r slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, r.Message)
	return nil
}

func (s *Sink) WithAttrs([]slog.Attr) slog.Handler { return s }

func (s *Sink) WithGroup(string) slog.Handler { return s }

func (s *Sink) Count(msg string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.msgs {
		if m == msg {
			n++
		}
	}
	return n
}
