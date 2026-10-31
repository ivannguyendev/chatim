package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/time/rate"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/ivannguyendev/chatim/pkg/corecall"
	"github.com/ivannguyendev/chatim/pkg/httpcors"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

const msgNotSupported = "request not supported"

type Options struct {
	Forwarder   *corecall.Forwarder
	Verifier    Verifier
	Revocations Revocations
	Gate        *userlimit.Gate
	Users       *userlimit.Users
	BodyMax     int
	CORSOrigins []string
	Log         *slog.Logger
}

type Handler struct {
	http.Handler
	stats *stats
}

func New(opts Options) (*Handler, error) {
	if opts.Forwarder == nil || opts.Verifier == nil || opts.Revocations == nil || opts.Gate == nil || opts.Users == nil || opts.Log == nil {
		return nil, errors.New("api: forwarder, verifier, revocations, gate, users and log are required")
	}
	if opts.BodyMax <= 0 {
		return nil, errors.New("api: body max must be positive")
	}
	st := newStats(opts.Gate.InFlight)
	c := &chain{
		verifier:    opts.Verifier,
		revocations: opts.Revocations,
		gate:        opts.Gate,
		users:       opts.Users,
		bodyMax:     int64(opts.BodyMax),
		stats:       st,
		log:         opts.Log,
		authLog:     rate.NewLimiter(rate.Every(time.Second), 1),
	}
	mux, err := c.procedures(opts.Forwarder, opts.BodyMax)
	if err != nil {
		return nil, err
	}
	inner := c.gated(c.authenticated(c.limited(c.measured(mux))))
	return &Handler{Handler: httpcors.New(httpcors.Config{
		Origins:       opts.CORSOrigins,
		AllowHeaders:  []string{"Authorization", "Content-Type", "Connect-Protocol-Version", "Connect-Timeout-Ms"},
		ExposeHeaders: []string{"Grpc-Status", "Grpc-Message", "Grpc-Status-Details-Bin"},
	}, inner), stats: st}, nil
}

func (h *Handler) Sources() []metrics.Source { return h.stats.sources() }

func (c *chain) procedures(fwd *corecall.Forwarder, bodyMax int) (*http.ServeMux, error) {
	mux := http.NewServeMux()
	for _, cmd := range fwd.Commands() {
		mt, err := protoregistry.GlobalTypes.FindMessageByName(cmd.Request.FullName())
		if err != nil {
			return nil, fmt.Errorf("api: request type of %s: %w", cmd.Method, err)
		}
		mux.Handle(cmd.Method, connect.NewUnaryHandler(cmd.Method, forward(fwd, cmd.Method),
			requestInitializer(mt),
			connect.WithCodec(protoCodec{}),
			connect.WithCodec(jsonCodec{}),
			connect.WithReadMaxBytes(bodyMax),
			connect.WithCompression("gzip", nil, nil),
		))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		c.stats.unknownMethod.Add(1)
		writeError(w, connect.CodeUnimplemented, msgNotSupported)
	})
	return mux, nil
}

func forward(fwd *corecall.Forwarder, method string) func(context.Context, *connect.Request[envelope]) (*connect.Response[envelope], error) {
	return func(ctx context.Context, req *connect.Request[envelope]) (*connect.Response[envelope], error) {
		claims, ok := ClaimsFrom(ctx)
		if !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, messageError(msgUnauthorized))
		}
		resp, outcome := fwd.Forward(ctx, claims, method, req.Msg.msg)
		if outcome.Code != codes.OK {
			return nil, connect.NewError(connectCode(outcome.Code), messageError(outcome.Message))
		}
		return connect.NewResponse(&envelope{msg: resp}), nil
	}
}
