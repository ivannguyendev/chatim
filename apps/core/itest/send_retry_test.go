package itest

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	itSendRetryLimit    = 5 * time.Second
	itSendRetryFirstGap = 20 * time.Millisecond
	itSendRetryMaxGap   = 400 * time.Millisecond
)

func sendRetrying(ctx context.Context, client chatimv1.CoreServiceClient, req *chatimv1.SendMessageRequest) (*chatimv1.SendMessageResponse, error) {
	return retryingUnavailable(ctx, func(ctx context.Context) (*chatimv1.SendMessageResponse, error) {
		return client.SendMessage(ctx, req)
	})
}

func retryingUnavailable[T any](ctx context.Context, do func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, itSendRetryLimit)
	defer cancel()
	gap := itSendRetryFirstGap
	for {
		out, err := do(ctx)
		if status.Code(err) != codes.Unavailable || !backoff.Pause(ctx, backoff.Jitter(gap)) {
			return out, err
		}
		gap = min(2*gap, itSendRetryMaxGap)
	}
}
