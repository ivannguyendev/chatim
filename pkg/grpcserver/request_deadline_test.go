package grpcserver_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/grpcserver"
)

func TestRequestDeadlineBoundsHandlersWithoutClientDeadline(t *testing.T) {
	const limit = 50 * time.Millisecond
	h := start(t, grpcserver.Config{RequestDeadline: limit}, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	begin := time.Now()
	err := callFake(t.Context(), h.conn)
	if got := status.Code(err); got != codes.DeadlineExceeded {
		t.Fatalf("call = %v, want DeadlineExceeded", err)
	}
	if took := time.Since(begin); took > 20*limit {
		t.Fatalf("call took %v, want about %v", took, limit)
	}
}

func TestRequestDeadlineKeepsAShorterClientDeadline(t *testing.T) {
	var left time.Duration
	h := start(t, grpcserver.Config{RequestDeadline: time.Hour}, func(ctx context.Context) error {
		deadline, _ := ctx.Deadline()
		left = time.Until(deadline)
		return nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := callFake(ctx, h.conn); err != nil {
		t.Fatalf("call: %v", err)
	}
	if left <= 0 || left > 6*time.Second {
		t.Fatalf("handler deadline in %v, want about the client's 5s", left)
	}
}

func TestNoRequestDeadlineLeavesHandlersUnbounded(t *testing.T) {
	var bounded bool
	h := start(t, grpcserver.Config{}, func(ctx context.Context) error {
		_, bounded = ctx.Deadline()
		return nil
	})
	if err := callFake(t.Context(), h.conn); err != nil {
		t.Fatalf("call: %v", err)
	}
	if bounded {
		t.Fatal("handler has a deadline, want none without RequestDeadline or a client deadline")
	}
}
