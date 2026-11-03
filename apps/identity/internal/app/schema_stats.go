package app

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

type schemaStats struct {
	mu       sync.Mutex
	outdated map[string]bool
	last     string
	seen     bool
}

func newSchemaStats() *schemaStats {
	return &schemaStats{outdated: map[string]bool{}}
}

func (s *schemaStats) note(ctx context.Context, log *slog.Logger, state store.SchemaState) {
	key := strings.Join(state.Missing, "\n") + "\x00" + strings.Join(state.Outdated, "\n")
	s.mu.Lock()
	changed := !s.seen || key != s.last
	s.seen, s.last = true, key
	s.outdated = map[string]bool{}
	for _, c := range state.Outdated {
		s.outdated[c] = true
	}
	s.mu.Unlock()
	if !changed {
		return
	}
	if len(state.Missing) > 0 {
		log.WarnContext(ctx, "identity schema incomplete, run /app migrate -apply", "missing", state.Missing)
	}
	for _, c := range state.Outdated {
		log.WarnContext(ctx, "index outdated, run /app migrate -apply", "collection", c)
	}
}

func (s *schemaStats) isOutdated(collection string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outdated[collection]
}

func (s *schemaStats) metricSources() []metrics.Source {
	names := mongostore.Indexes().Names()
	out := make([]metrics.Source, 0, len(names))
	for _, name := range names {
		out = append(out, metrics.Source{
			Name: "index_outdated", Help: "1 when the collection has an index out of date with the binary; run /app migrate -apply.", Gauge: true,
			Labels: map[string]string{"collection": name}, Read: func() float64 { return boolValue(s.isOutdated(name)) },
		})
	}
	return out
}
