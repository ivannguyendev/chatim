package eventkit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

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

type pullSource struct {
	js       ConsumerLookup
	stream   string
	name     string
	consumer jetstream.Consumer
}

func NewPullSource(js ConsumerLookup, stream, consumer string) Source {
	return &pullSource{js: js, stream: stream, name: consumer}
}

func (s *pullSource) Fetch(ctx context.Context, limit int, wait time.Duration) ([]Delivery, error) {
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
	fctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	batch, err := s.consumer.Fetch(limit, jetstream.FetchContext(fctx))
	if err != nil {
		return nil, fmt.Errorf("fetch from %s: %w", s.name, err)
	}
	var out []Delivery
	for m := range batch.Messages() {
		out = append(out, pullDelivery{m})
	}
	return out, idleFetch(ctx, batch.Error())
}

func idleFetch(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return nil
	}
	return err
}

type pullDelivery struct {
	jetstream.Msg
}

func (d pullDelivery) Delivered() uint64 {
	meta, err := d.Metadata()
	if err != nil {
		return 0
	}
	return meta.NumDelivered
}

func (d pullDelivery) Nak(delay time.Duration) error { return d.NakWithDelay(delay) }
