package app

import (
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/identity/internal/api"
	"github.com/ivannguyendev/chatim/apps/identity/internal/keyring"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
)

type labelled struct {
	label string
	count *atomic.Int64
}

type keyStats struct {
	activated atomic.Int64
}

type cleanupStats struct {
	removed atomic.Int64
	errors  atomic.Int64
}

func metricSources(ring *keyring.Ring, internal *api.InternalStats, cleanup *cleanupStats, keys *keyStats) []metrics.Source {
	var out []metrics.Source
	counter := func(name, help, label string, series []labelled) {
		for _, s := range series {
			c := s.count
			src := metrics.Source{Name: name, Help: help, Read: func() float64 { return float64(c.Load()) }}
			if label != "" {
				src.Labels = map[string]string{label: s.label}
			}
			out = append(out, src)
		}
	}
	counter("revocations_cleaned_total", "Expired revoked-session rows deleted.", "", []labelled{{"", &cleanup.removed}})
	counter("revocation_cleanup_errors_total", "Failed deletes of expired revoked-session rows.", "", []labelled{{"", &cleanup.errors}})
	counter("signing_keys_activated_total", "Pending signing keys activated by identity once IDENTITY_KEY_ACTIVATE_LEAD passed.", "", []labelled{{"", &keys.activated}})
	for _, s := range []struct {
		label string
		count *atomic.Uint64
	}{{"ok", &internal.OK}, {"not_modified", &internal.NotModified}, {"unauthorized", &internal.Unauthorized}, {"error", &internal.Errors}} {
		c := s.count
		out = append(out, metrics.Source{Name: "revocation_requests_total", Help: "Revocation list requests on the internal port by result.",
			Labels: map[string]string{"result": s.label}, Read: func() float64 { return float64(c.Load()) }})
	}
	out = append(out,
		metrics.Source{Name: "signing_key_ready", Help: "1 when an active signing key is loaded.", Gauge: true, Read: func() float64 {
			if ring.Ready() {
				return 1
			}
			return 0
		}},
	)
	return out
}

func jitBatchSources(st Store) []metrics.Source {
	var out []metrics.Source
	for writer, read := range map[string]func() metrics.Histogram{"links": st.JITLinkBatchDocs, "users": st.JITUserBatchDocs} {
		out = append(out, metrics.Source{Name: "jit_batch_docs", Help: "Documents per just-in-time InsertMany sent to MongoDB, by writer.",
			Labels: map[string]string{"writer": writer}, Histogram: read})
	}
	return out
}
