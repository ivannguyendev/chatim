package route

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type rpc[T any] func(ctx context.Context, api chatimv1.CoreServiceClient) (T, error)

func call[T any](ctx context.Context, c *Client, pick func() (string, bool), retryable func(codes.Code) bool, do rpc[T]) (T, Stats, error) {
	ctx, cancel := context.WithTimeout(ctx, c.policy.Deadline)
	defer cancel()
	var st Stats
	delay := c.policy.FirstDelay
	for {
		st.Attempts++
		out, err := attempt(ctx, c, pick, &st, do)
		if err == nil {
			return out, st, nil
		}
		if !retryable(status.Code(err)) || ctx.Err() != nil {
			return out, st, fmt.Errorf("after %d attempts: %w", st.Attempts, err)
		}
		_ = c.loc.Refresh(ctx)
		if !backoff.Pause(ctx, backoff.Jitter(delay)) {
			return out, st, fmt.Errorf("deadline reached after %d attempts: %w", st.Attempts, err)
		}
		delay = min(2*delay, c.policy.MaxDelay)
	}
}

func attempt[T any](ctx context.Context, c *Client, pick func() (string, bool), st *Stats, do rpc[T]) (T, error) {
	var zero T
	addr, ok := pick()
	if !ok {
		return zero, errNoRoute
	}
	st.Addr = addr
	api, err := c.api(addr)
	if err != nil {
		return zero, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.policy.Attempt)
	defer cancel()
	return do(ctx, api)
}

func retryUnavailable(code codes.Code) bool { return code == codes.Unavailable }

func retryIdempotent(code codes.Code) bool {
	switch code {
	case codes.Unavailable, codes.ResourceExhausted, codes.DeadlineExceeded, codes.Aborted:
		return true
	default:
		return false
	}
}
