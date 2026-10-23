package corecall

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/callerid"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

var (
	errUnknownMethod = status.Error(codes.Unimplemented, "method not served")
	errMalformed     = status.Error(codes.InvalidArgument, "malformed request")
	errEmptyResult   = status.Error(codes.Internal, "core returned no result")
)

type Route func(cmd Command, req proto.Message) (Conn, error)

type Check func(req proto.Message) error

type Options struct {
	Methods       []string
	Deadline      time.Duration
	Route         Route
	Checks        map[string]Check
	OnUnavailable func()
}

type Forwarder struct {
	commands      []Command
	byMethod      map[string]int
	checks        map[string]Check
	route         Route
	deadline      time.Duration
	onUnavailable func()
	stats         *stats
}

func New(opts Options) (*Forwarder, error) {
	if opts.Route == nil {
		return nil, errors.New("corecall: route is required")
	}
	if opts.Deadline <= 0 {
		return nil, errors.New("corecall: core request deadline must be positive")
	}
	cmds, err := Commands(opts.Methods)
	if err != nil {
		return nil, err
	}
	byMethod := make(map[string]int, len(cmds))
	names := make([]string, len(cmds))
	for i, c := range cmds {
		byMethod[c.Method] = i
		names[i] = c.Name
	}
	checks := make(map[string]Check, len(opts.Checks))
	for m, check := range opts.Checks {
		if _, ok := byMethod[m]; !ok {
			return nil, fmt.Errorf("corecall: check for %s, which is not served", m)
		}
		checks[m] = check
	}
	return &Forwarder{
		commands:      cmds,
		byMethod:      byMethod,
		checks:        checks,
		route:         opts.Route,
		deadline:      opts.Deadline,
		onUnavailable: opts.OnUnavailable,
		stats:         newStats(names),
	}, nil
}

func (f *Forwarder) Commands() []Command { return append([]Command(nil), f.commands...) }

func (f *Forwarder) Forward(ctx context.Context, claims jwtverify.Claims, method string, req proto.Message) (proto.Message, Outcome) {
	i, ok := f.byMethod[method]
	if !ok {
		return nil, Outcome{Code: codes.Unimplemented, Detail: errUnknownMethod.Error(), Message: ClientMessage(codes.Unimplemented, "")}
	}
	cmd := f.commands[i]
	result, err := f.run(ctx, claims, i, req)
	if err != nil {
		code, coreMessage := codeOf(err)
		f.stats.count(i, code)
		if code == codes.Unavailable && f.onUnavailable != nil {
			f.onUnavailable()
		}
		return nil, Outcome{Command: cmd.Name, Code: code, Detail: err.Error(), Message: ClientMessage(code, coreMessage)}
	}
	f.stats.count(i, codes.OK)
	return result, Outcome{Command: cmd.Name, Code: codes.OK}
}

func (f *Forwarder) CommandSources() []metrics.Source { return f.stats.commandSources() }

func (f *Forwarder) CoreCallLatency() []CommandLatency { return f.stats.latencies() }

func (f *Forwarder) run(ctx context.Context, claims jwtverify.Claims, i int, inner proto.Message) (proto.Message, error) {
	cmd := f.commands[i]
	if inner == nil || inner.ProtoReflect().Descriptor().FullName() != cmd.Request.FullName() {
		return nil, errWrongRequest
	}
	req, err := CopyKnown(inner)
	if err != nil {
		return nil, err
	}
	if check := f.checks[cmd.Method]; check != nil {
		if err := check(req); err != nil {
			return nil, err
		}
	}
	conn, err := f.route(cmd, req)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, f.deadline)
	defer cancel()
	callCtx = callerid.WithCaller(metadata.NewOutgoingContext(callCtx, metadata.MD{}), claims.Tenant, claims.User)
	start := time.Now()
	result, err := cmd.call(callCtx, conn, req)
	f.stats.observe(i, time.Since(start))
	if err == nil && !result.ProtoReflect().IsValid() {
		return nil, errEmptyResult
	}
	return result, err
}

func CopyKnown(m proto.Message) (proto.Message, error) {
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
