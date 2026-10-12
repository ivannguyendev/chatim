package work_test

import (
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestRealWorkQueueDefersRecordsOfUnknownKinds(t *testing.T) {
	js, cfg := realWork(t)
	data := work.Encode(work.Record{Kind: store.RoomInserted, Room: 5, CommittedAt: time.Now()})
	data[0] = 13
	future := &nats.Msg{Subject: work.Subject(cfg.SubjectRoot, 3), Data: data, Header: nats.Header{}}
	future.Header.Set(jetstream.MsgIDHeader, "future-1")
	if _, err := js.PublishMsg(t.Context(), future); err != nil {
		t.Fatalf("publish: %v", err)
	}
	q := work.NewQueue(js, cfg.Name, 3, 200*time.Millisecond)
	for round := range 2 {
		ds, err := q.Fetch(t.Context(), 1, 2*time.Second)
		var bad work.BadRecordsError
		if len(ds) != 0 || !errors.As(err, &bad) || bad.Deferred != 1 || bad.Terminated != 0 {
			t.Fatalf("fetch %d = %d deliveries, %v; want one deferred record that comes back", round, len(ds), err)
		}
	}
}
