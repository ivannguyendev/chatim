package jsconsume_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/core/jsconsume"
)

type fakeIterator struct {
	msgs     []jetstream.Msg
	err      error
	drained  int
	stopped  int
	nextOpts int
}

func (it *fakeIterator) Next(opts ...jetstream.NextOpt) (jetstream.Msg, error) {
	it.nextOpts = len(opts)
	if len(it.msgs) == 0 {
		return nil, it.err
	}
	m := it.msgs[0]
	it.msgs = it.msgs[1:]
	return m, nil
}

func (it *fakeIterator) Drain() { it.drained++ }

func (it *fakeIterator) Stop() { it.stopped++ }

type streamConsumer struct {
	jetstream.Consumer
	it   *fakeIterator
	opts []jetstream.PullMessagesOpt
}

func (c *streamConsumer) Messages(opts ...jetstream.PullMessagesOpt) (jetstream.MessagesContext, error) {
	c.opts = opts
	return c.it, nil
}

func TestStreamSourceAsksForChunksAndReadsDeliveries(t *testing.T) {
	it := &fakeIterator{msgs: []jetstream.Msg{message(3)}, err: jetstream.ErrMsgIteratorClosed}
	c := &streamConsumer{it: it}
	s, err := jsconsume.NewStreamSource(fixedLookup{c}, "ORDERS", "billing", 512).Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(c.opts) != 1 || c.opts[0] != jetstream.PullMaxMessages(512) {
		t.Fatalf("Messages options %v, want one PullMaxMessages(512)", c.opts)
	}
	d, err := s.Next(t.Context())
	if err != nil || d.Delivered() != 3 || it.nextOpts != 1 {
		t.Fatalf("Next = %v, %v with %d options; want delivery 3 bound to the context", d, err, it.nextOpts)
	}
	if _, err := s.Next(t.Context()); !errors.Is(err, jsconsume.ErrStreamClosed) || !errors.Is(err, jetstream.ErrMsgIteratorClosed) {
		t.Fatalf("Next after close = %v, want ErrStreamClosed", err)
	}
	s.Drain()
	s.Stop()
	if it.drained != 1 || it.stopped != 1 {
		t.Errorf("drained %d stopped %d, want each once", it.drained, it.stopped)
	}
}

func TestStreamSourcePassesOtherErrorsAndAtLeastOneMessage(t *testing.T) {
	it := &fakeIterator{err: context.DeadlineExceeded}
	c := &streamConsumer{it: it}
	s, err := jsconsume.NewStreamSource(fixedLookup{c}, "ORDERS", "billing", 0).Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if c.opts[0] != jetstream.PullMaxMessages(1) {
		t.Errorf("chunk %v, want 1", c.opts[0])
	}
	if _, err := s.Next(t.Context()); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, jsconsume.ErrStreamClosed) {
		t.Errorf("Next = %v, want the iterator error as is", err)
	}
}

func TestStreamSourceReportsAFailedLookupAndSkipsAnEndedContext(t *testing.T) {
	l := &refusingLookup{}
	src := jsconsume.NewStreamSource(l, "ORDERS", "billing", 8)
	if _, err := src.Open(t.Context()); !errors.Is(err, errLookup) || l.stream != "ORDERS" || l.name != "billing" {
		t.Fatalf("Open = %v on %s/%s, want the lookup error for ORDERS/billing", err, l.stream, l.name)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := src.Open(ctx); !errors.Is(err, context.Canceled) || l.calls != 1 {
		t.Errorf("Open on an ended context = %v after %d lookups", err, l.calls)
	}
}
