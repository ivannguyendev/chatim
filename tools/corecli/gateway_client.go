package main

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/v1/chatimv1connect"
)

const defaultGatewayURL = "http://chatim-gateway-1:8080"

type gateway struct {
	base    context.Context
	timeout time.Duration
	core    chatimv1connect.CoreServiceClient
	mention chatimv1connect.MentionServiceClient
}

func newGateway(base context.Context, url, token string, timeout time.Duration) *gateway {
	bearer := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", "Bearer "+token)
			return next(ctx, req)
		}
	})
	client := &http.Client{Timeout: timeout}
	return &gateway{
		base:    base,
		timeout: timeout,
		core:    chatimv1connect.NewCoreServiceClient(client, url, connect.WithInterceptors(bearer)),
		mention: chatimv1connect.NewMentionServiceClient(client, url, connect.WithInterceptors(bearer)),
	}
}

func viaGateway[Req, Res any](g *gateway, call func(context.Context, *connect.Request[Req]) (*connect.Response[Res], error), req *Req) (*Res, error) {
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(g.base, g.timeout)
		resp, err := call(ctx, connect.NewRequest(req))
		cancel()
		if err == nil {
			return resp.Msg, nil
		}
		if connect.CodeOf(err) != connect.CodeUnavailable || attempt == wsCallAttempts {
			return nil, err
		}
		if !backoff.Pause(g.base, wsRetryPause) {
			return nil, g.base.Err()
		}
	}
}
