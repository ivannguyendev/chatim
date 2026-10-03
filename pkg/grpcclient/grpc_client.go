package grpcclient

import (
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
)

const DefaultServiceConfig = `{
  "loadBalancingConfig": [{"round_robin": {}}],
  "retryThrottling": {"maxTokens": 10, "tokenRatio": 0.1},
  "methodConfig": [{
    "name": [{"service": "grpc.health.v1.Health", "method": "Check"}],
    "timeout": "2s",
    "retryPolicy": {
      "maxAttempts": 3,
      "initialBackoff": "0.1s",
      "maxBackoff": "1s",
      "backoffMultiplier": 2,
      "retryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`

type Options struct {
	Creds         credentials.TransportCredentials
	ServiceConfig string
	DialOptions   []grpc.DialOption
}

func New(target string, opts Options) (*grpc.ClientConn, error) {
	if opts.Creds == nil {
		return nil, errors.New("grpcclient: Options.Creds is required")
	}
	sc := opts.ServiceConfig
	if sc == "" {
		sc = DefaultServiceConfig
	}

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(opts.Creds),
		grpc.WithChainUnaryInterceptor(wrapDownstreamUnary),
		grpc.WithDefaultServiceConfig(sc),
		grpc.WithDisableServiceConfig(),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}
	dialOpts = append(dialOpts, opts.DialOptions...)

	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("grpc client %q: %w", target, err)
	}
	return conn, nil
}
