package work

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type fakeMsg struct {
	jetstream.Msg
	data    []byte
	settled []string
	delay   time.Duration
}

func (m *fakeMsg) Data() []byte { return m.data }

func (m *fakeMsg) Term() error {
	m.settled = append(m.settled, "term")
	return nil
}

func (m *fakeMsg) NakWithDelay(d time.Duration) error {
	m.settled, m.delay = append(m.settled, "nak"), d
	return nil
}

type fakeBatch struct{ msgs chan jetstream.Msg }

func (b fakeBatch) Messages() <-chan jetstream.Msg { return b.msgs }

func (fakeBatch) Error() error { return nil }

func batchOf(msgs ...*fakeMsg) fakeBatch {
	ch := make(chan jetstream.Msg, len(msgs))
	for _, m := range msgs {
		ch <- m
	}
	close(ch)
	return fakeBatch{msgs: ch}
}

func TestCollectTermsMalformedAndDefersUnknownKinds(t *testing.T) {
	good := &fakeMsg{data: Encode(Record{Kind: store.MessageInserted, Room: 1, Seq: 1})}
	future := Encode(Record{Kind: store.RoomInserted, Room: 2})
	future[0] = 9
	unknown, junk := &fakeMsg{data: future}, &fakeMsg{data: []byte("junk")}
	ds, err := collect(t.Context(), batchOf(good, unknown, junk), 7*time.Second)
	var bad BadRecordsError
	if len(ds) != 1 || ds[0].Record().Room != 1 || !errors.As(err, &bad) || bad != (BadRecordsError{Terminated: 1, Deferred: 1}) || !errors.Is(err, ErrBadRecord) {
		t.Fatalf("collect = %d deliveries, %v; want 1 delivery, 1 terminated and 1 deferred", len(ds), err)
	}
	if !slices.Equal(unknown.settled, []string{"nak"}) || unknown.delay != 7*time.Second {
		t.Fatalf("unknown kind settled %v after %v, want one nak after the retry delay", unknown.settled, unknown.delay)
	}
	if !slices.Equal(junk.settled, []string{"term"}) || len(good.settled) != 0 {
		t.Fatalf("junk settled %v, good settled %v; want term and nothing", junk.settled, good.settled)
	}
}

func TestCollectReportsNothingWhenEveryRecordDecodes(t *testing.T) {
	ds, err := collect(t.Context(), batchOf(&fakeMsg{data: Encode(Record{Kind: store.RoomInserted, Room: 3})}), time.Second)
	if len(ds) != 1 || err != nil {
		t.Fatalf("collect = %d deliveries, %v; want 1 and no error", len(ds), err)
	}
}
