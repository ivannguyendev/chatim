package eventkit_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
)

type fakeMsg struct {
	jetstream.Msg
	meta *jetstream.MsgMetadata
}

func (m fakeMsg) Metadata() (*jetstream.MsgMetadata, error) { return m.meta, nil }

type fakeBatch struct{ msgs chan jetstream.Msg }

func (b fakeBatch) Messages() <-chan jetstream.Msg { return b.msgs }
func (b fakeBatch) Error() error                   { return nil }

type fakeConsumer struct {
	jetstream.Consumer
	batch fakeBatch
	floor uint64
}

func (c *fakeConsumer) Fetch(int, ...jetstream.FetchOpt) (jetstream.MessageBatch, error) {
	return c.batch, nil
}

func (c *fakeConsumer) Info(context.Context) (*jetstream.ConsumerInfo, error) {
	return &jetstream.ConsumerInfo{AckFloor: jetstream.SequenceInfo{Stream: c.floor}}, nil
}

type fixedLookup struct{ c jetstream.Consumer }

func (l fixedLookup) Consumer(context.Context, string, string) (jetstream.Consumer, error) {
	return l.c, nil
}

func message(stream, delivered uint64) jetstream.Msg {
	return fakeMsg{meta: &jetstream.MsgMetadata{Sequence: jetstream.SequencePair{Stream: stream}, NumDelivered: delivered}}
}

func TestPullSourceReadsAStoppedFetchToItsEndAndReportsTheFloor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &fakeConsumer{batch: fakeBatch{msgs: make(chan jetstream.Msg, 4)}, floor: 41}
		src := eventkit.NewPullSource(fixedLookup{c}, "CHATIM_EVT", "projections")
		if floor, err := src.AckFloor(t.Context()); err != nil || floor != 41 {
			t.Fatalf("AckFloor = %d, %v; want 41", floor, err)
		}
		c.batch.msgs <- message(42, 1)
		ctx, cancel := context.WithCancel(t.Context())
		type result struct {
			ds  []eventkit.Delivery
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
		c.batch.msgs <- message(43, 2)
		close(c.batch.msgs)
		got := <-done
		if !errors.Is(got.err, context.Canceled) || len(got.ds) != 2 {
			t.Fatalf("Fetch = %d deliveries, %v; want both and context.Canceled", len(got.ds), got.err)
		}
		if d := got.ds[1]; d.StreamSeq() != 43 || d.Delivered() != 2 {
			t.Errorf("delivery stream seq %d delivered %d, want 43 and 2", d.StreamSeq(), d.Delivered())
		}
	})
}
