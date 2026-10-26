package app

import (
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/guard"
	"github.com/ivannguyendev/chatim/apps/iam/internal/keyring"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

type labelled struct {
	label string
	count *atomic.Int64
}

func metricSources(st *auth.Stats, slots *guard.HashSlots, ring *keyring.Ring) []metrics.Source {
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
	counter("login_total", "Password checks by result.", "result", []labelled{
		{"ok", &st.LoginOK}, {"invalid", &st.LoginInvalid}, {"throttled_account", &st.LoginThrottledAccount},
		{"throttled_ip", &st.LoginThrottledIP}, {"busy", &st.LoginBusy}, {"must_change", &st.LoginMustChange},
	})
	counter("refresh_total", "Refresh token requests by result.", "result", []labelled{
		{"ok", &st.RefreshOK}, {"grace", &st.RefreshGrace}, {"reuse", &st.RefreshReuse}, {"unknown", &st.RefreshUnknown},
		{"key_mismatch", &st.RefreshKeyMismatch}, {"expired", &st.RefreshExpired}, {"revoked", &st.RefreshRevoked},
		{"replay", &st.RefreshReplay}, {"unavailable", &st.RefreshUnavailable},
	})
	counter("sessions_revoked_total", "Sessions revoked by logout, user action or token reuse.", "", []labelled{{"", &st.SessionsRevoked}})
	counter("password_changes_total", "Successful password changes.", "", []labelled{{"", &st.PasswordChanged}})
	counter("store_errors_total", "Postgres errors seen by the auth use cases.", "", []labelled{{"", &st.StoreErrors}})
	out = append(out,
		metrics.Source{Name: "hash_inflight", Help: "Password hashes running now.", Gauge: true, Read: func() float64 { return float64(slots.InUse()) }},
		metrics.Source{Name: "signing_key_ready", Help: "1 when an active signing key is loaded.", Gauge: true, Read: func() float64 {
			if ring.Ready() {
				return 1
			}
			return 0
		}},
	)
	return out
}
