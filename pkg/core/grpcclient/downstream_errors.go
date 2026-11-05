package grpcclient

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
)

func wrapDownstreamUnary(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if err := invoker(ctx, method, req, reply, cc, opts...); err != nil {
		return fmt.Errorf("call %s: %w", method, err)
	}
	return nil
}
