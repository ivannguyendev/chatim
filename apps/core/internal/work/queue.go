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
}

func (e BadRecordsError) Error() string {
	return strconv.FormatUint(e.Terminated, 10) + " undecodable work records terminated"
}

func (BadRecordsError) Unwrap() error { return ErrBadRecord }

type jetStreamQueue struct {
	js        jetstream.JetStream
	stream    string
	partition int
	consumer  jetstream.Consumer
}

func NewQueue(js jetstream.JetStream, stream string, partition int) Queue {
	return &jetStreamQueue{js: js, stream: stream, partition: partition}
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
	batch, err := q.consumer.Fetch(limit, jetstream.FetchMaxWait(wait))
	if err != nil {
		return nil, fmt.Errorf("fetch work partition %d: %w", q.partition, err)
	}
	return collect(ctx, batch)
}

func collect(ctx context.Context, batch jetstream.MessageBatch) ([]Delivery, error) {
	var out []Delivery
	var bad uint64
	msgs := batch.Messages()
	for {
		select {
		case m, open := <-msgs:
			if !open {
				return out, errors.Join(batch.Error(), badRecords(bad))
			}
			r, err := Decode(m.Data())
			if err != nil {
				_ = m.Term()
				bad++
				continue
			}
			out = append(out, jetStreamDelivery{msg: m, rec: r})
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
}

func badRecords(n uint64) error {
	if n == 0 {
		return nil
	}
	return BadRecordsError{Terminated: n}
}

type jetStreamDelivery struct {
	msg jetstream.Msg
	rec Record
}

func (d jetStreamDelivery) Record() Record { return d.rec }

func (d jetStreamDelivery) Ack() error { return d.msg.Ack() }

func (d jetStreamDelivery) Nak(delay time.Duration) error { return d.msg.NakWithDelay(delay) }
