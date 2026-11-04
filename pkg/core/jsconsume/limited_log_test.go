package jsconsume_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type countingHandler struct {
	mu   sync.Mutex
	msgs map[string]int
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs[r.Message]++
	return nil
}

func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *countingHandler) WithGroup(string) slog.Handler { return h }

func (h *countingHandler) count(msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.msgs[msg]
}

func TestLimitedLogWarnsAtMostOncePerSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &countingHandler{msgs: map[string]int{}}
		l := jsconsume.NewLimitedLog(slog.New(sink))
		l.Warn(t.Context(), "boom")
		l.Warn(t.Context(), "boom")
		time.Sleep(999 * time.Millisecond)
		l.Warn(t.Context(), "boom")
		if got := sink.count("boom"); got != 1 {
			t.Fatalf("warnings within one second = %d, want 1", got)
		}
		time.Sleep(time.Millisecond)
		l.Warn(t.Context(), "boom")
		if got := sink.count("boom"); got != 2 {
			t.Fatalf("warnings after one second = %d, want 2", got)
		}
	})
}
