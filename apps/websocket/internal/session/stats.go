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

type authResult int

const (
	authOK authResult = iota
	authInvalid
	authExpired
	authRate
	authUserLimit
	authResultCount
)

const (
	otherLabel           = "other"
	reasonIdentityChange = "identity_changed"
	reasonUnknown        = "unknown"
)

var (
	rejectLabels     = [rejectReasonCount]string{"rate", "inflight", "unimplemented", "shutdown"}
	authResultLabels = [authResultCount]string{"ok", "invalid", "expired", "rate", "user_limit"}
	closeCodes       = []uint16{
		CodeNormal, CodeGoingAway, CodeProtocolError, CodeUnsupportedData, CodeAbnormal, CodeInvalidPayload,
		CodePolicyViolation, CodeTooLarge, CodeInternalError, CodeTryAgainLater, CodeUnauthenticated,
		CodeSlowClient, CodeTooManyConnections,
	}
)

type stats struct {
	open                atomic.Int64
	unauth              atomic.Int64
	queuedBytes         atomic.Int64
	unknownFrames       atomic.Uint64
	unauthOverCap       atomic.Uint64
	authRequiredExpired atomic.Uint64
	rejected            [rejectReasonCount]atomic.Uint64
	auths               [authResultCount]atomic.Uint64
	closes              map[string]*atomic.Uint64
}

func newStats() *stats {
	s := &stats{closes: map[string]*atomic.Uint64{}}
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

func (s *stats) authed(result authResult) { s.auths[result].Add(1) }

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
		{Name: "unauthenticated_connections", Help: "Open websocket connections waiting for a valid token.", Gauge: true, Read: gaugeOf(&s.unauth)},
		{Name: "send_queue_bytes", Help: "Bytes queued for writing across open websocket connections.", Gauge: true, Read: gaugeOf(&s.queuedBytes)},
		{Name: "unknown_frames_total", Help: "Client frames of an unknown type ignored.", Read: counterOf(&s.unknownFrames)},
		{Name: "unauth_over_cap_total", Help: "Connections that went back to waiting for a token above WS_UNAUTH_MAX.", Read: counterOf(&s.unauthOverCap)},
		{
			Name: "auth_required_total", Help: "auth_required frames sent to running connections, by reason.",
			Labels: map[string]string{"reason": "expired"}, Read: counterOf(&s.authRequiredExpired),
		},
	}
	for result := range authResultCount {
		out = append(out, metrics.Source{
			Name: "auth_total", Help: "Auth frames, by result.",
			Labels: map[string]string{"result": authResultLabels[result]}, Read: counterOf(&s.auths[result]),
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
