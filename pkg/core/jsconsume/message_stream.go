package jsconsume

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

var ErrStreamClosed = errors.New("message stream closed")

type Stream interface {
	Next(ctx context.Context) (Delivery, error)
	Drain()
	Stop()
}

type StreamOpener interface {
	Open(ctx context.Context) (Stream, error)
}

type StreamSource struct {
	js     ConsumerLookup
	stream string
	name   string
	chunk  int
}

func NewStreamSource(js ConsumerLookup, stream, consumer string, chunk int) *StreamSource {
	return &StreamSource{js: js, stream: stream, name: consumer, chunk: max(chunk, 1)}
}

func (s *StreamSource) Open(ctx context.Context) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := s.js.Consumer(ctx, s.stream, s.name)
	if err != nil {
		return nil, fmt.Errorf("consumer %s on %s: %w", s.name, s.stream, err)
	}
	it, err := c.Messages(jetstream.PullMaxMessages(s.chunk))
	if err != nil {
		return nil, fmt.Errorf("messages from %s: %w", s.name, err)
	}
	return messageStream{it: it}, nil
}

type messageStream struct {
	it jetstream.MessagesContext
}

func (m messageStream) Next(ctx context.Context) (Delivery, error) {
	msg, err := m.it.Next(jetstream.NextContext(ctx))
	switch {
	case err == nil:
		return newPullDelivery(msg), nil
	case errors.Is(err, jetstream.ErrMsgIteratorClosed):
		return nil, fmt.Errorf("%w: %w", ErrStreamClosed, err)
	}
	return nil, err
}

func (m messageStream) Drain() { m.it.Drain() }

func (m messageStream) Stop() { m.it.Stop() }
