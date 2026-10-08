package effects

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type pendingAck struct {
	index  int
	future jetstream.PubAckFuture
}

func send(js publish.JetStream, msg *nats.Msg, i int, errs []error, pending []pendingAck) []pendingAck {
	f, err := js.PublishMsgAsync(msg)
	if err != nil {
		errs[i] = err
		return pending
	}
	return append(pending, pendingAck{index: i, future: f})
}

func awaitAcks(ctx context.Context, pending []pendingAck, errs []error, count func(*jetstream.PubAck)) {
	for _, p := range pending {
		select {
		case ack := <-p.future.Ok():
			count(ack)
		case err := <-p.future.Err():
			errs[p.index] = err
		case <-ctx.Done():
			errs[p.index] = ctx.Err()
		}
	}
}

func countAll(n *atomic.Uint64) func(*jetstream.PubAck) {
	return func(*jetstream.PubAck) { n.Add(1) }
}

func countStored(n *atomic.Uint64) func(*jetstream.PubAck) {
	return func(ack *jetstream.PubAck) {
		if ack != nil && !ack.Duplicate {
			n.Add(1)
		}
	}
}

func undeliverable(err error) bool {
	return errors.Is(err, domain.ErrRoomNotFound) || errors.Is(err, apperr.ErrInvalidArgument)
}
