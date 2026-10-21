package session

import (
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

type rejectReason int

const (
	rejectRate rejectReason = iota
	rejectInflight
	rejectUnimplemented
	rejectShutdown
	rejectReasonCount
)

const (
	otherLabel           = "other"
	reasonIdentityChange = "identity_changed"
	reasonUnknown        = "unknown"
)

var (
	rejectLabels = [rejectReasonCount]string{"rate", "inflight", "unimplemented", "shutdown"}
	closeCodes   = []uint16{
		CodeNormal, CodeGoingAway, CodeProtocolError, CodeUnsupportedData, CodeAbnormal, CodeInvalidPayload,
		CodePolicyViolation, CodeTooLarge, CodeInternalError, CodeUnauthenticated, CodeAuthTimeout,
	}
	tokenReasons = []jwtverify.Reason{
		jwtverify.ReasonMalformed, jwtverify.ReasonAlgorithm, jwtverify.ReasonSignature, jwtverify.ReasonKeyID,
		jwtverify.ReasonType, jwtverify.ReasonIssuer, jwtverify.ReasonAudience, jwtverify.ReasonExpired,
		jwtverify.ReasonNotYetValid, jwtverify.ReasonMissingClaim, jwtverify.ReasonSubject, jwtverify.ReasonTenant,
		jwtverify.ReasonClaims,
	}
)

type stats struct {
	open          atomic.Int64
	unauth        atomic.Int64
	unknownFrames atomic.Uint64
	rejected      [rejectReasonCount]atomic.Uint64
	authFailures  map[string]*atomic.Uint64
	closes        map[string]*atomic.Uint64
}

func newStats() *stats {
	s := &stats{authFailures: map[string]*atomic.Uint64{}, closes: map[string]*atomic.Uint64{}}
	for _, reason := range tokenReasons {
		s.authFailures[reasonLabel(reason)] = new(atomic.Uint64)
	}
	s.authFailures[reasonIdentityChange] = new(atomic.Uint64)
	s.authFailures[reasonUnknown] = new(atomic.Uint64)
	for _, code := range closeCodes {
		s.closes[strconv.Itoa(int(code))] = new(atomic.Uint64)
	}
	s.closes[otherLabel] = new(atomic.Uint64)
	return s
}

func reasonLabel(reason jwtverify.Reason) string {
	if reason == "" {
		return reasonUnknown
	}
	return strings.ReplaceAll(string(reason), " ", "_")
}

func (s *stats) authFailed(label string) {
	counter, ok := s.authFailures[label]
	if !ok {
		counter = s.authFailures[reasonUnknown]
	}
	counter.Add(1)
}

func (s *stats) closed(code uint16) {
	counter, ok := s.closes[strconv.Itoa(int(code))]
	if !ok {
		counter = s.closes[otherLabel]
	}
	counter.Add(1)
}

func (s *stats) reject(reason rejectReason) { s.rejected[reason].Add(1) }

func (s *stats) sources() []metrics.Source {
	out := []metrics.Source{
		{Name: "connections", Help: "Open websocket connections.", Gauge: true, Read: gaugeOf(&s.open)},
		{Name: "unauthenticated_connections", Help: "Open websocket connections that have not authenticated yet.", Gauge: true, Read: gaugeOf(&s.unauth)},
		{Name: "unknown_frames_total", Help: "Client frames of an unknown type ignored after auth.", Read: counterOf(&s.unknownFrames)},
	}
	for label, counter := range s.authFailures {
		out = append(out, metrics.Source{
			Name: "auth_failures_total", Help: "Rejected auth frames, by reason.",
			Labels: map[string]string{"reason": label}, Read: counterOf(counter),
		})
	}
	for label, counter := range s.closes {
		out = append(out, metrics.Source{
			Name: "closes_total", Help: "Connections closed by the server, by close code.",
			Labels: map[string]string{"code": label}, Read: counterOf(counter),
		})
	}
	for reason := range rejectReasonCount {
		out = append(out, metrics.Source{
			Name: "commands_rejected_total", Help: "Client requests answered without calling core, by reason.",
			Labels: map[string]string{"reason": rejectLabels[reason]}, Read: counterOf(&s.rejected[reason]),
		})
	}
	return out
}

func gaugeOf(v *atomic.Int64) func() float64 { return func() float64 { return float64(v.Load()) } }

func counterOf(v *atomic.Uint64) func() float64 { return func() float64 { return float64(v.Load()) } }
