package api

import (
	"sync/atomic"

	"github.com/ivannguyendev/chatim/pkg/auth"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
)

const (
	reasonMissing       = "missing"
	reasonRepeated      = "repeated"
	reasonTooLong       = "too long"
	reasonBadValue      = "bad header"
	reasonOther         = "other"
	reasonRevoked       = "revoked"
	reasonTenantRevoked = "tenant_revoked"
	reasonKeyPending    = "key_pending"
	limitRate           = "rate"
	limitInflight       = "inflight"
)

var authReasons = []string{
	reasonMissing, reasonRepeated, reasonTooLong, reasonBadValue, reasonOther,
	string(auth.ReasonMalformed), string(auth.ReasonAlgorithm), string(auth.ReasonSignature),
	string(auth.ReasonKeyID), string(auth.ReasonType), string(auth.ReasonIssuer),
	string(auth.ReasonAudience), string(auth.ReasonExpired), string(auth.ReasonNotYetValid),
	string(auth.ReasonMissingClaim), string(auth.ReasonSubject), string(auth.ReasonTenant),
	string(auth.ReasonClaims), string(auth.ReasonSession), reasonRevoked, reasonTenantRevoked, reasonKeyPending,
}

type stats struct {
	auth          map[string]*atomic.Uint64
	rateLimited   atomic.Uint64
	userInflight  atomic.Uint64
	nodeInflight  atomic.Uint64
	bodyTooLarge  atomic.Uint64
	unknownMethod atomic.Uint64
	inflight      func() int
}

func newStats(inflight func() int) *stats {
	s := &stats{auth: make(map[string]*atomic.Uint64, len(authReasons)), inflight: inflight}
	for _, r := range authReasons {
		s.auth[r] = &atomic.Uint64{}
	}
	return s
}

func (s *stats) authFailed(reason string) string {
	c, ok := s.auth[reason]
	if !ok {
		reason, c = reasonOther, s.auth[reasonOther]
	}
	c.Add(1)
	return reason
}

func counter(name, help string, labels map[string]string, c *atomic.Uint64) metrics.Source {
	return metrics.Source{Name: name, Help: help, Labels: labels, Read: func() float64 { return float64(c.Load()) }}
}

func (s *stats) sources() []metrics.Source {
	out := make([]metrics.Source, 0, len(authReasons)+6)
	for _, r := range authReasons {
		out = append(out, counter("auth_failures_total", "Requests refused before any chat call because the token was missing or invalid, by reason.", map[string]string{"reason": r}, s.auth[r]))
	}
	return append(out,
		counter("rate_limited_total", "Requests refused by the per-user limits, by limit.", map[string]string{"limit": limitRate}, &s.rateLimited),
		counter("rate_limited_total", "Requests refused by the per-user limits, by limit.", map[string]string{"limit": limitInflight}, &s.userInflight),
		counter("inflight_rejected_total", "Requests refused because the node already had GW_INFLIGHT_MAX requests in flight.", nil, &s.nodeInflight),
		counter("body_too_large_total", "Requests whose body was larger than APP_BODY_SIZE_MAX.", nil, &s.bodyTooLarge),
		counter("commands_total", "Client commands handled, by command and result code.", map[string]string{"command": "unknown", "code": "Unimplemented"}, &s.unknownMethod),
		metrics.Source{Name: "inflight", Help: "Requests in flight on this node.", Gauge: true, Read: func() float64 { return float64(s.inflight()) }},
	)
}
