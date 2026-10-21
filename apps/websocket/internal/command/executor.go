package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/ivannguyendev/chatim/pkg/coreroute"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errUnknownRequest = status.Error(codes.Unimplemented, "request type not set or not known")
	errMalformed      = status.Error(codes.InvalidArgument, "malformed request")
	errEmptyResult    = status.Error(codes.Internal, "core returned no result")
)

type Router interface {
	Room(roomID string) (coreroute.Conn, error)
	Any() (coreroute.Conn, error)
	Trigger()
}

type Executor struct {
	router   Router
	deadline time.Duration
	commands map[protoreflect.FieldNumber]*command
	stats    *stats
}

func New(router Router, deadline time.Duration) (*Executor, error) {
	if router == nil {
		return nil, errors.New("command: router is required")
	}
	if deadline <= 0 {
		return nil, errors.New("command: core request deadline must be positive")
	}
	cmds, err := buildCommands()
	if err != nil {
		return nil, err
	}
	byNumber := make(map[protoreflect.FieldNumber]*command, len(cmds))
	names := make([]string, len(cmds))
	for _, cmd := range cmds {
		byNumber[cmd.result.Number()] = cmd
		names[cmd.index] = cmd.name
	}
	return &Executor{router: router, deadline: deadline, commands: byNumber, stats: newStats(names)}, nil
}

func (e *Executor) Execute(ctx context.Context, claims jwtverify.Claims, req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, Outcome) {
	resp := &chatimv1.ServerResponse{Id: req.GetId()}
	msg := req.ProtoReflect()
	field := msg.WhichOneof(requestOneof)
	if field == nil {
		return failed(resp, Outcome{Code: codes.Unimplemented, Detail: errUnknownRequest.Error()}, "")
	}
	cmd := e.commands[field.Number()]
	result, err := e.run(ctx, claims, cmd, msg.Get(field).Message().Interface())
	if err != nil {
		code, coreMessage := codeOf(err)
		e.stats.count(cmd.index, code)
		if code == codes.Unavailable {
			e.router.Trigger()
		}
		return failed(resp, Outcome{Command: cmd.name, Code: code, Detail: err.Error()}, coreMessage)
	}
	e.stats.count(cmd.index, codes.OK)
	resp.ProtoReflect().Set(cmd.result, protoreflect.ValueOfMessage(result.ProtoReflect()))
	return resp, Outcome{Command: cmd.name, Code: codes.OK}
}

func (e *Executor) CommandSources() []metrics.Source { return e.stats.commandSources() }

func (e *Executor) CoreCallLatency() []CommandLatency { return e.stats.latencies() }

func (e *Executor) run(ctx context.Context, claims jwtverify.Claims, cmd *command, inner proto.Message) (proto.Message, error) {
	req, err := copyKnownFields(inner)
	if err != nil {
		return nil, err
	}
	if cmd.check != nil {
		if err := cmd.check(req); err != nil {
			return nil, err
		}
	}
	conn, err := e.route(cmd, req)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, e.deadline)
	defer cancel()
	callCtx = coreroute.WithCaller(metadata.NewOutgoingContext(callCtx, metadata.MD{}), claims.Tenant, claims.User)
	start := time.Now()
	result, err := cmd.call(callCtx, conn, req)
	e.stats.observe(cmd.index, time.Since(start))
	if err == nil && !result.ProtoReflect().IsValid() {
		return nil, errEmptyResult
	}
	return result, err
}

func (e *Executor) route(cmd *command, req proto.Message) (coreroute.Conn, error) {
	if cmd.room == nil {
		return e.router.Any()
	}
	return e.router.Room(req.ProtoReflect().Get(cmd.room).String())
}

func copyKnownFields(m proto.Message) (proto.Message, error) {
	raw, err := proto.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errMalformed, err)
	}
	out := m.ProtoReflect().New().Interface()
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: %w", errMalformed, err)
	}
	return out, nil
}

func failed(resp *chatimv1.ServerResponse, outcome Outcome, coreMessage string) (*chatimv1.ServerResponse, Outcome) {
	resp.Code = wireCode(outcome.Code)
	resp.Message = clientMessage(outcome.Code, coreMessage)
	return resp, outcome
}

func wireCode(code codes.Code) int32 {
	if code > codes.Unauthenticated {
		return int32(codes.Unknown)
	}
	return int32(code)
}
