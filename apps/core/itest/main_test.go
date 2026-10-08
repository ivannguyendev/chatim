package itest

import (
	"log/slog"
	"testing"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
)

func TestMain(m *testing.M) {
	testlog.SilenceRedis()
	goleak.VerifyTestMain(m)
}

var quiet = slog.New(slog.DiscardHandler)

func built[T any](v T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return v
	}
}
