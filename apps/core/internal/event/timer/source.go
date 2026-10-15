package timer

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

type jetStreamSource struct {
	js       ConsumerLookup
	stream   string
	consumer jetstream.Consumer
}

func NewSource(js ConsumerLookup, stream string) Source {
	return &jetStreamSource{js: js, stream: stream}
}

func (s *jetStreamSource) Fetch(ctx context.Context, limit int, wait time.Duration) ([]Delivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.consumer == nil {
		c, err := s.js.Consumer(ctx, s.stream, ConsumerName)
		if err != nil {
			return nil, fmt.Errorf("timer consumer %s on %s: %w", ConsumerName, s.stream, err)
		}
		s.consumer = c
	}
	fctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	batch, err := s.consumer.Fetch(limit, jetstream.FetchContext(fctx))
	if err != nil {
		return nil, fmt.Errorf("fetch due timers: %w", err)
	}
	var out []Delivery
	for m := range batch.Messages() {
		out = append(out, jetStreamDelivery{m})
	}
	return out, fetchError(ctx, batch.Error())
}

func fetchError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return nil
	}
	return err
}

type jetStreamDelivery struct {
	jetstream.Msg
}

func (d jetStreamDelivery) Delivered() uint64 {
	meta, err := d.Metadata()
	if err != nil {
		return 0
	}
	return meta.NumDelivered
}

func (d jetStreamDelivery) Nak(delay time.Duration) error { return d.NakWithDelay(delay) }
