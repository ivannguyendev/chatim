package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/command"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const Subprotocol = "chatim.v1"

const (
	CodeNormal             uint16 = 1000
	CodeGoingAway          uint16 = 1001
	CodeProtocolError      uint16 = 1002
	CodeUnsupportedData    uint16 = 1003
	CodeAbnormal           uint16 = 1006
	CodeInvalidPayload     uint16 = 1007
	CodePolicyViolation    uint16 = 1008
	CodeTooLarge           uint16 = 1009
	CodeInternalError      uint16 = 1011
	CodeTryAgainLater      uint16 = 1013
	CodeUnauthenticated    uint16 = 4001
	CodeSlowClient         uint16 = 4008
	CodeTooManyConnections uint16 = 4009
)

type Verifier interface {
	Verify(token string) (jwtverify.Claims, error)
}

type Executor interface {
	Execute(ctx context.Context, claims jwtverify.Claims, req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, command.Outcome)
}

type Interest interface {
	Register(tenant, user string, sink interest.Sink) (*interest.Conn, error)
}

type Typing interface {
	FromClient(conn *interest.Conn, tenant, user string, f *chatimv1.TypingFrame)
	RateLimited()
}

type Presence interface {
	Touch(tenant, user string, now time.Time)
	Answer(ctx context.Context, tenant string, users []string) ([]*chatimv1.PresenceEntry, error)
}

type Options struct {
	AuthGrace        time.Duration
	AuthRate         int
	AuthBurst        int
	AuthRetryMin     time.Duration
	AuthRetryMax     time.Duration
	HeartbeatTimeout time.Duration
	JWTLeeway        time.Duration
	WriteTimeout     time.Duration
	UnauthMax        int
	FrameMax         int
	CommandRate      int
	CommandBurst     int
	CommandInflight  int
	SendQueue        int
	SendQueueBytes   int
	TypingRate       int
	TypingBurst      int
	PresenceWatchMax int
}

func (o Options) validate() error {
	var errs []error
	positive := func(name string, ok bool) {
		if !ok {
			errs = append(errs, fmt.Errorf("session: %s must be positive", name))
		}
	}
	positive("auth grace", o.AuthGrace > 0)
	positive("auth rate", o.AuthRate > 0)
	positive("auth burst", o.AuthBurst > 0)
	positive("auth retry min", o.AuthRetryMin > 0)
	positive("auth retry max", o.AuthRetryMax > 0)
	positive("heartbeat timeout", o.HeartbeatTimeout > 0)
	positive("write timeout", o.WriteTimeout > 0)
	positive("unauthenticated connection limit", o.UnauthMax > 0)
	positive("frame limit", o.FrameMax > 0)
	positive("command rate", o.CommandRate > 0)
	positive("command burst", o.CommandBurst > 0)
	positive("commands in flight", o.CommandInflight > 0)
	positive("send queue", o.SendQueue > 0)
	positive("send queue bytes", o.SendQueueBytes > 0)
	positive("typing rate", o.TypingRate > 0)
	positive("typing burst", o.TypingBurst > 0)
	positive("presence watch limit", o.PresenceWatchMax > 0)
	if o.SendQueueBytes < o.FrameMax {
		errs = append(errs, errors.New("session: send queue bytes must hold one frame of the frame limit"))
	}
	if o.AuthRetryMin > o.AuthRetryMax {
		errs = append(errs, errors.New("session: auth retry min must not exceed auth retry max"))
	}
	if o.JWTLeeway < 0 {
		errs = append(errs, errors.New("session: jwt leeway must not be negative"))
	}
	return errors.Join(errs...)
}
