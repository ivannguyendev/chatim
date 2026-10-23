package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/ivannguyendev/chatim/pkg/corecall"
	"github.com/ivannguyendev/chatim/pkg/coreroute"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errUnknownRequest = status.Error(codes.Unimplemented, "request type not set or not known")
	errBadRoom        = status.Error(codes.InvalidArgument, "invalid room_id")
)

type (
	Outcome        = corecall.Outcome
	CommandLatency = corecall.CommandLatency
)

type Router interface {
	Room(roomID string) (coreroute.Conn, error)
	Any() (coreroute.Conn, error)
	Trigger()
}

type Executor struct {
	router   Router
	forward  *corecall.Forwarder
	commands map[protoreflect.FieldNumber]command
}

func New(router Router, deadline time.Duration) (*Executor, error) {
	if router == nil {
		return nil, errors.New("command: router is required")
	}
	served, err := corecall.Commands(servedMethods())
	if err != nil {
		return nil, err
	}
	cmds, methods, err := buildCommands(served)
	if err != nil {
		return nil, err
	}
	e := &Executor{router: router, commands: cmds}
	e.forward, err = corecall.New(corecall.Options{
		Methods:       methods,
		Deadline:      deadline,
		Route:         e.route,
		Checks:        checks(),
		OnUnavailable: router.Trigger,
	})
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Executor) Execute(ctx context.Context, claims jwtverify.Claims, req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, Outcome) {
	resp := &chatimv1.ServerResponse{Id: req.GetId()}
	msg := req.ProtoReflect()
	field := msg.WhichOneof(requestOneof)
	if field == nil {
		return failed(resp, Outcome{Code: codes.Unimplemented, Detail: errUnknownRequest.Error(), Message: corecall.ClientMessage(codes.Unimplemented, "")})
	}
	cmd := e.commands[field.Number()]
	result, outcome := e.forward.Forward(ctx, claims, cmd.method, msg.Get(field).Message().Interface())
	if outcome.Code != codes.OK {
		return failed(resp, outcome)
	}
	resp.ProtoReflect().Set(cmd.result, protoreflect.ValueOfMessage(result.ProtoReflect()))
	return resp, outcome
}

func (e *Executor) CommandSources() []metrics.Source { return e.forward.CommandSources() }

func (e *Executor) CoreCallLatency() []CommandLatency { return e.forward.CoreCallLatency() }

func (e *Executor) route(cmd corecall.Command, req proto.Message) (corecall.Conn, error) {
	roomID, ok := cmd.RoomID(req)
	if !ok {
		return conn(e.router.Any())
	}
	if _, err := coreroute.ParseRoom(roomID); err != nil {
		return corecall.Conn{}, fmt.Errorf("%w: %w", errBadRoom, err)
	}
	return conn(e.router.Room(roomID))
}

func conn(c coreroute.Conn, err error) (corecall.Conn, error) {
	if err != nil {
		return corecall.Conn{}, err
	}
	return corecall.Conn{Core: c.Core, Mention: c.Mention}, nil
}

func failed(resp *chatimv1.ServerResponse, outcome Outcome) (*chatimv1.ServerResponse, Outcome) {
	resp.Code = wireCode(outcome.Code)
	resp.Message = outcome.Message
	return resp, outcome
}

func wireCode(code codes.Code) int32 {
	if code > codes.Unauthenticated {
		return int32(codes.Unknown)
	}
	return int32(code)
}
