package eventkit

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const FetchExpiryGrace = time.Second

type Delivery interface {
	Data() []byte
	Delivered() uint64
	Ack() error
	Nak(delay time.Duration) error
	Term() error
}

type Source interface {
	Fetch(ctx context.Context, limit int, wait time.Duration) ([]Delivery, error)
}

type ConsumerLookup interface {
	Consumer(ctx context.Context, stream, name string) (jetstream.Consumer, error)
}

type PullSource struct {
	js       ConsumerLookup
	stream   string
	name     string
	consumer jetstream.Consumer
}

func NewPullSource(js ConsumerLookup, stream, consumer string) *PullSource {
	return &PullSource{js: js, stream: stream, name: consumer}
}

func (s *PullSource) Fetch(ctx context.Context, limit int, wait time.Duration) ([]Delivery, error) {
	c, err := s.lookup(ctx)
	if err != nil {
		return nil, err
	}
	batch, err := c.Fetch(limit, jetstream.FetchMaxWait(wait))
	if err != nil {
		return nil, fmt.Errorf("fetch from %s: %w", s.name, err)
	}
	var out []Delivery
	msgs := batch.Messages()
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				return out, batch.Error()
			}
			out = append(out, newPullDelivery(m))
		case <-ctx.Done():
			for m := range msgs {
				out = append(out, newPullDelivery(m))
			}
			return out, ctx.Err()
		}
	}
}

func (s *PullSource) lookup(ctx context.Context) (jetstream.Consumer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.consumer == nil {
		c, err := s.js.Consumer(ctx, s.stream, s.name)
		if err != nil {
			return nil, fmt.Errorf("consumer %s on %s: %w", s.name, s.stream, err)
		}
		s.consumer = c
	}
	return s.consumer, nil
}

type pullDelivery struct {
	jetstream.Msg
	delivered uint64
}

func newPullDelivery(m jetstream.Msg) pullDelivery {
	d := pullDelivery{Msg: m}
	if meta, err := m.Metadata(); err == nil {
		d.delivered = meta.NumDelivered
	}
	return d
}

func (d pullDelivery) Delivered() uint64 { return d.delivered }

func (d pullDelivery) Nak(delay time.Duration) error { return d.NakWithDelay(delay) }
