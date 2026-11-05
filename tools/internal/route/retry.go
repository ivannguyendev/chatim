package route

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	"github.com/ivannguyendev/chatim/pkg/core/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type rpc[T any] func(ctx context.Context, api chatimv1.ChatServiceClient) (T, error)

type connRPC[T any] func(ctx context.Context, conn chatrpc.PoolConn) (T, error)

func call[T any](ctx context.Context, c *Client, pick func() (chatrpc.PoolConn, error), do connRPC[T]) (T, Stats, error) {
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
		if !retryable(err) || ctx.Err() != nil {
			return out, st, fmt.Errorf("after %d attempts: %w", st.Attempts, err)
		}
		_ = c.routes.Refresh(ctx)
		if !backoff.Pause(ctx, backoff.Jitter(delay)) {
			return out, st, fmt.Errorf("deadline reached after %d attempts: %w", st.Attempts, err)
		}
		delay = min(2*delay, c.policy.MaxDelay)
	}
}

func attempt[T any](ctx context.Context, c *Client, pick func() (chatrpc.PoolConn, error), st *Stats, do connRPC[T]) (T, error) {
	conn, err := pick()
	if conn.Addr != "" {
		st.Addr = conn.Addr
	}
	if err != nil {
		var zero T
		return zero, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.policy.Attempt)
	defer cancel()
	return do(ctx, conn)
}

func inRoom[T any](ctx context.Context, c *Client, roomID string, do rpc[T]) (T, Stats, error) {
	if _, err := chatrpc.ParseRoom(roomID); err != nil {
		var zero T
		return zero, Stats{}, err
	}
	return call(ctx, c, func() (chatrpc.PoolConn, error) { return c.routes.Room(roomID) }, onChatNode(do))
}

func anyChatNode[T any](ctx context.Context, c *Client, do rpc[T]) (T, Stats, error) {
	return call(ctx, c, c.routes.Any, onChatNode(do))
}

func onChatNode[T any](do rpc[T]) connRPC[T] {
	return func(ctx context.Context, conn chatrpc.PoolConn) (T, error) { return do(ctx, conn.Chat) }
}

func retryable(err error) bool {
	if errors.Is(err, chatrpc.ErrDial) || errors.Is(err, chatrpc.ErrTooManyAddresses) {
		return false
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.ResourceExhausted, codes.DeadlineExceeded, codes.Aborted:
		return true
	default:
		return false
	}
}
