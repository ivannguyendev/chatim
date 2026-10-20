package roomcache

import (
	"log/slog"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

var quiet = slog.New(slog.DiscardHandler)

type counted interface {
	Hits() uint64
	Misses() uint64
}

func expectStats(t *testing.T, name string, s counted, hits, misses uint64) {
	t.Helper()
	if s.Hits() != hits || s.Misses() != misses {
		t.Errorf("%s hits/misses = %d/%d, want %d/%d", name, s.Hits(), s.Misses(), hits, misses)
	}
}
