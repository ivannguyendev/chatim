package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type JetStream interface {
	PublishMsgAsync(m *nats.Msg, opts ...jetstream.PublishOpt) (jetstream.PubAckFuture, error)
}

type Publisher struct {
	js    JetStream
	stats *Stats
}

func NewPublisher(js JetStream, stats *Stats) *Publisher {
	return &Publisher{js: js, stats: stats}
}

func (p *Publisher) Batch() *Batch { return &Batch{p: p} }

type pending struct {
	kind   Kind
	id     string
	users  uint64
	future jetstream.PubAckFuture
}

type Batch struct {
	p       *Publisher
	pending []pending
	err     error
}

func (b *Batch) Add(n Notice) error {
	id := n.Packet.GetId()
	if id == "" || n.Subject == "" {
		return b.fail(fmt.Errorf("%w: ws publish packet needs an id and a subject", apperr.ErrInvalidArgument))
	}
	data, err := proto.Marshal(n.Packet)
	if err != nil {
		return b.fail(fmt.Errorf("marshal ws publish packet %s: %w", id, err))
	}
	m := &nats.Msg{Subject: n.Subject, Data: data, Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, id)
	f, err := b.p.js.PublishMsgAsync(m)
	if err != nil {
		return b.fail(fmt.Errorf("publish ws publish packet %s: %w", id, err))
	}
	b.pending = append(b.pending, pending{kind: n.Kind, id: id, users: uint64(len(n.Packet.GetTargets().GetUsers())), future: f})
	return nil
}

func (b *Batch) Wait(ctx context.Context) error {
	errs := []error{b.err}
	for i, w := range b.pending {
		select {
		case ack := <-w.future.Ok():
			b.count(w, ack)
		case err := <-w.future.Err():
			b.p.stats.failures.Add(1)
			errs = append(errs, fmt.Errorf("ws publish packet %s: %w", w.id, err))
		case <-ctx.Done():
			b.pending = b.pending[i:]
			return errors.Join(append(errs, ctx.Err())...)
		}
	}
	b.pending = nil
	return errors.Join(errs...)
}

func (b *Batch) fail(err error) error {
	b.p.stats.failures.Add(1)
	b.err = errors.Join(b.err, err)
	return err
}

func (b *Batch) count(w pending, ack *jetstream.PubAck) {
	if ack != nil && ack.Duplicate {
		return
	}
	switch w.kind {
	case KindNotified:
		b.p.stats.notified.Add(w.users)
	case KindRetracted:
		b.p.stats.retracted.Add(w.users)
	}
}
