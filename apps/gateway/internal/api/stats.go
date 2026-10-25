package api

import (
	"sync/atomic"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

const (
	reasonMissing  = "missing"
	reasonRepeated = "repeated"
	reasonTooLong  = "too long"
	reasonBadValue = "bad header"
	reasonOther    = "other"
	limitRate      = "rate"
	limitInflight  = "inflight"
)

var authReasons = []string{
	reasonMissing, reasonRepeated, reasonTooLong, reasonBadValue, reasonOther,
	string(jwtverify.ReasonMalformed), string(jwtverify.ReasonAlgorithm), string(jwtverify.ReasonSignature),
	string(jwtverify.ReasonKeyID), string(jwtverify.ReasonType), string(jwtverify.ReasonIssuer),
	string(jwtverify.ReasonAudience), string(jwtverify.ReasonExpired), string(jwtverify.ReasonNotYetValid),
	string(jwtverify.ReasonMissingClaim), string(jwtverify.ReasonSubject), string(jwtverify.ReasonTenant),
	string(jwtverify.ReasonClaims),
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
		out = append(out, counter("auth_failures_total", "Requests refused before any core call because the token was missing or invalid, by reason.", map[string]string{"reason": r}, s.auth[r]))
	}
	return append(out,
		counter("rate_limited_total", "Requests refused by the per-user limits, by limit.", map[string]string{"limit": limitRate}, &s.rateLimited),
		counter("rate_limited_total", "Requests refused by the per-user limits, by limit.", map[string]string{"limit": limitInflight}, &s.userInflight),
		counter("inflight_rejected_total", "Requests refused because the node already had GW_INFLIGHT_MAX requests in flight.", nil, &s.nodeInflight),
		counter("body_too_large_total", "Requests whose body was larger than GW_BODY_MAX.", nil, &s.bodyTooLarge),
		counter("commands_total", "Client commands handled, by command and result code.", map[string]string{"command": "unknown", "code": "Unimplemented"}, &s.unknownMethod),
		metrics.Source{Name: "inflight", Help: "Requests in flight on this node.", Gauge: true, Read: func() float64 { return float64(s.inflight()) }},
	)
}
