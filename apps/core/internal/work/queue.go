package work

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type Delivery interface {
	Record() Record
	Ack() error
	Nak(delay time.Duration) error
}

type Queue interface {
	Fetch(ctx context.Context, max int, wait time.Duration) ([]Delivery, error)
}

type BadRecordsError struct {
	Terminated uint64
	Deferred   uint64
}

func (e BadRecordsError) Error() string {
	return strconv.FormatUint(e.Terminated, 10) + " undecodable work records terminated, " +
		strconv.FormatUint(e.Deferred, 10) + " records of unknown kinds deferred"
}

func (e BadRecordsError) orNil() error {
	if e.Terminated == 0 && e.Deferred == 0 {
		return nil
	}
	return e
}

func (BadRecordsError) Unwrap() error { return ErrBadRecord }

type jetStreamQueue struct {
	js         jetstream.JetStream
	stream     string
	partition  int
	retryDelay time.Duration
	consumer   jetstream.Consumer
}

func NewQueue(js jetstream.JetStream, stream string, partition int, retryDelay time.Duration) Queue {
	return &jetStreamQueue{js: js, stream: stream, partition: partition, retryDelay: retryDelay}
}

func (q *jetStreamQueue) Fetch(ctx context.Context, limit int, wait time.Duration) ([]Delivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q.consumer == nil {
		c, err := q.js.Consumer(ctx, q.stream, ConsumerName(q.partition))
		if err != nil {
			return nil, fmt.Errorf("work consumer %s on %s: %w", ConsumerName(q.partition), q.stream, err)
		}
		q.consumer = c
	}
	fctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	batch, err := q.consumer.Fetch(limit, jetstream.FetchContext(fctx))
	if err != nil {
		return nil, fmt.Errorf("fetch work partition %d: %w", q.partition, err)
	}
	return collect(ctx, batch, q.retryDelay)
}

func collect(ctx context.Context, batch jetstream.MessageBatch, retryDelay time.Duration) ([]Delivery, error) {
	var out []Delivery
	var bad BadRecordsError
	msgs := batch.Messages()
	for {
		select {
		case m, open := <-msgs:
			if !open {
				return out, errors.Join(fetchError(ctx, batch.Error()), bad.orNil())
			}
			r, err := Decode(m.Data())
			switch {
			case errors.Is(err, ErrUnknownKind):
				_ = m.NakWithDelay(retryDelay)
				bad.Deferred++
			case err != nil:
				_ = m.Term()
				bad.Terminated++
			default:
				out = append(out, jetStreamDelivery{msg: m, rec: r})
			}
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
}

func fetchError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return nil
	}
	return err
}

type jetStreamDelivery struct {
	msg jetstream.Msg
	rec Record
}

func (d jetStreamDelivery) Record() Record { return d.rec }

func (d jetStreamDelivery) Ack() error { return d.msg.Ack() }

func (d jetStreamDelivery) Nak(delay time.Duration) error { return d.msg.NakWithDelay(delay) }
