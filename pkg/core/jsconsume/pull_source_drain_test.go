package jsconsume_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

type fakeMsg struct {
	jetstream.Msg
	meta     *jetstream.MsgMetadata
	progress *int
}

func (m fakeMsg) Metadata() (*jetstream.MsgMetadata, error) { return m.meta, nil }

func (m fakeMsg) InProgress() error {
	*m.progress++
	return nil
}

type fakeBatch struct{ msgs chan jetstream.Msg }

func (b fakeBatch) Messages() <-chan jetstream.Msg { return b.msgs }
func (b fakeBatch) Error() error                   { return nil }

type fakeConsumer struct {
	jetstream.Consumer
	batch fakeBatch
}

func (c *fakeConsumer) Fetch(int, ...jetstream.FetchOpt) (jetstream.MessageBatch, error) {
	return c.batch, nil
}

type fixedLookup struct{ c jetstream.Consumer }

func (l fixedLookup) Consumer(context.Context, string, string) (jetstream.Consumer, error) {
	return l.c, nil
}

func message(delivered uint64) jetstream.Msg {
	return fakeMsg{meta: &jetstream.MsgMetadata{NumDelivered: delivered}, progress: new(int)}
}

func TestPullSourceReadsAStoppedFetchToItsEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &fakeConsumer{batch: fakeBatch{msgs: make(chan jetstream.Msg, 4)}}
		src := jsconsume.NewPullSource(fixedLookup{c}, "CHATIM_EVT", "projections")
		c.batch.msgs <- message(1)
		ctx, cancel := context.WithCancel(t.Context())
		type result struct {
			ds  []jsconsume.Delivery
			err error
		}
		done := make(chan result, 1)
		go func() {
			ds, err := src.Fetch(ctx, 8, time.Second)
			done <- result{ds, err}
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("Fetch returned while the pull was still open, want it read to the server expiry")
		default:
		}
		c.batch.msgs <- message(2)
		close(c.batch.msgs)
		got := <-done
		if !errors.Is(got.err, context.Canceled) || len(got.ds) != 2 {
			t.Fatalf("Fetch = %d deliveries, %v; want both and context.Canceled", len(got.ds), got.err)
		}
		if d := got.ds[1]; d.Delivered() != 2 {
			t.Errorf("delivered %d, want 2", d.Delivered())
		}
	})
}

func TestPullDeliveryReportsProgressToItsMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := message(1)
		c := &fakeConsumer{batch: fakeBatch{msgs: make(chan jetstream.Msg, 1)}}
		c.batch.msgs <- m
		close(c.batch.msgs)
		ds, err := jsconsume.NewPullSource(fixedLookup{c}, "CHATIM_EVT", "mention").Fetch(t.Context(), 8, time.Second)
		if err != nil || len(ds) != 1 {
			t.Fatalf("Fetch = %d deliveries, %v; want one", len(ds), err)
		}
		if err := ds[0].InProgress(); err != nil || *m.(fakeMsg).progress != 1 {
			t.Errorf("InProgress = %v after %d calls, want one call on the message", err, *m.(fakeMsg).progress)
		}
	})
}
