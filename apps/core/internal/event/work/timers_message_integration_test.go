package work_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestRealJetStreamFiresAMessageCountCheckOnTheRoomPartition(t *testing.T) {
	js, cfg := realWork(t)
	room := roomsOnPartition(itTimerPartition, cfg.Partitions, 1)[0]
	tm := itTimers(t, js, cfg, &testlog.Sink{})
	armedAt := time.Now()
	key := store.MsgKey{Room: room, Seq: 9}
	if _, err := tm.ArmMessageCountCheck(t.Context(), key, "replies"); err != nil {
		t.Fatalf("ArmMessageCountCheck: %v", err)
	}
	if _, err := tm.ArmMemberCountCheck(t.Context(), room); err != nil {
		t.Fatalf("ArmMemberCountCheck: %v", err)
	}
	ds, err := work.NewQueue(js, cfg.Name, itTimerPartition, time.Second).Fetch(t.Context(), 2, itTimerDelay+3*time.Second)
	if err != nil || len(ds) != 2 {
		t.Fatalf("Fetch after %v = %d deliveries, %v; want both fired timers", time.Since(armedAt), len(ds), err)
	}
	kinds := map[store.ChangeKind]work.Record{}
	for _, d := range ds {
		kinds[d.Record().Kind] = d.Record()
		if err := d.Ack(); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	got, ok := kinds[store.MessageCountCheck]
	if !ok || got.Room != room || got.Thread != 0 || got.Seq != 9 || got.User != "replies" || got.CommittedAt.Before(armedAt.Add(itTimerDelay).Truncate(time.Second)) {
		t.Fatalf("fired records = %+v, want a replies count check of %d/3/9 stamped with its fire time", kinds, room)
	}
	if _, ok := kinds[store.MemberCountCheck]; !ok {
		t.Fatalf("fired records = %+v, want the member count check too", kinds)
	}
	awaitEmpty(t, js, cfg.Name)
}
