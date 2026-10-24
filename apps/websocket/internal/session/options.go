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

const (
	Subprotocol   = "chatim.v1"
	authReadGrace = time.Second
)

const (
	CodeNormal          uint16 = 1000
	CodeGoingAway       uint16 = 1001
	CodeProtocolError   uint16 = 1002
	CodeUnsupportedData uint16 = 1003
	CodeAbnormal        uint16 = 1006
	CodeInvalidPayload  uint16 = 1007
	CodePolicyViolation uint16 = 1008
	CodeTooLarge        uint16 = 1009
	CodeInternalError   uint16 = 1011
	CodeTryAgainLater   uint16 = 1013
	CodeUnauthenticated uint16 = 4001
	CodeAuthTimeout     uint16 = 4002
	CodeSlowClient      uint16 = 4008
)

type Verifier interface {
	Verify(token string) (jwtverify.Claims, error)
}

type Executor interface {
	Execute(ctx context.Context, claims jwtverify.Claims, req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, command.Outcome)
}

type Interest interface {
	Register(tenant, user string, sink interest.Sink) *interest.Conn
}

type Typing interface {
	FromClient(conn *interest.Conn, tenant, user string, f *chatimv1.TypingFrame)
	RateLimited()
}

type Options struct {
	AuthTimeout      time.Duration
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
}

func (o Options) validate() error {
	var errs []error
	positive := func(name string, ok bool) {
		if !ok {
			errs = append(errs, fmt.Errorf("session: %s must be positive", name))
		}
	}
	positive("auth timeout", o.AuthTimeout > 0)
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
	if o.SendQueueBytes < o.FrameMax {
		errs = append(errs, errors.New("session: send queue bytes must hold one frame of the frame limit"))
	}
	if o.JWTLeeway < 0 {
		errs = append(errs, errors.New("session: jwt leeway must not be negative"))
	}
	return errors.Join(errs...)
}
