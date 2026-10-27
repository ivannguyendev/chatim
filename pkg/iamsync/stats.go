package iamsync

import (
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/pkg/metrics"
)

type stats struct {
	listErrors        atomic.Uint64
	keyErrors         atomic.Uint64
	keyConflicts      atomic.Uint64
	sharedMerged      atomic.Uint64
	sharedUnchanged   atomic.Uint64
	sharedErrors      atomic.Uint64
	sharedWriteErrors atomic.Uint64
	bootTimeouts      atomic.Uint64
}

func (s *Syncer) Sources() []metrics.Source {
	counter := func(name, help string, labels map[string]string, c *atomic.Uint64) metrics.Source {
		return metrics.Source{Name: name, Help: help, Labels: labels, Read: func() float64 { return float64(c.Load()) }}
	}
	gauge := func(name, help string, labels map[string]string, read func() float64) metrics.Source {
		return metrics.Source{Name: name, Help: help, Labels: labels, Gauge: true, Read: read}
	}
	enabled := func(on bool) func() float64 {
		return func() float64 {
			if on {
				return 1
			}
			return 0
		}
	}
	st := &s.stats
	return []metrics.Source{
		gauge("iam_sync_enabled", "1 when this node asks iam for this kind.", map[string]string{"kind": "revocations"}, enabled(s.cfg.InternalURL != "")),
		gauge("iam_sync_enabled", "1 when this node asks iam for this kind.", map[string]string{"kind": "keys"}, enabled(s.cfg.JWKSURL != "")),
		gauge("iam_revocations_age_seconds", "Seconds since the revocation list was last refreshed from iam or the shared copy (since start before the first refresh).", nil, s.age(func() time.Time { return s.lastList })),
		gauge("iam_keys_age_seconds", "Seconds since keys were last fetched from iam (since start before the first fetch).", nil, s.age(func() time.Time { return s.lastKeys })),
		counter("iam_sync_errors_total", "Failed fetches from iam by kind.", map[string]string{"kind": "revocations"}, &st.listErrors),
		counter("iam_sync_errors_total", "Failed fetches from iam by kind.", map[string]string{"kind": "keys"}, &st.keyErrors),
		counter("jwks_conflicts_total", "Keys from iam dropped because the key file holds the same kid with another key.", nil, &st.keyConflicts),
		counter("iam_shared_reads_total", "Reads of the shared revocation copy after an iam failure by result.", map[string]string{"result": "merged"}, &st.sharedMerged),
		counter("iam_shared_reads_total", "Reads of the shared revocation copy after an iam failure by result.", map[string]string{"result": "unchanged"}, &st.sharedUnchanged),
		counter("iam_shared_reads_total", "Reads of the shared revocation copy after an iam failure by result.", map[string]string{"result": "error"}, &st.sharedErrors),
		counter("iam_shared_write_errors_total", "Failed writes of the shared revocation copy.", nil, &st.sharedWriteErrors),
		counter("iam_boot_timeouts_total", "Boots that went ready before the first iam sync completed.", nil, &st.bootTimeouts),
		gauge("revocations", "Revocation entries held by this node.", map[string]string{"kind": "session"}, func() float64 { n, _ := s.list.Counts(); return float64(n) }),
		gauge("revocations", "Revocation entries held by this node.", map[string]string{"kind": "tenant"}, func() float64 { _, n := s.list.Counts(); return float64(n) }),
		{Name: "revocations_dropped_total", Help: "Revocation entries refused as out of bounds.", Read: func() float64 { return float64(s.list.Dropped()) }},
	}
}

func (s *Syncer) age(last func() time.Time) func() float64 {
	return func() float64 {
		s.mu.Lock()
		at := last()
		s.mu.Unlock()
		if at.IsZero() {
			at = s.started
		}
		return time.Since(at).Seconds()
	}
}
