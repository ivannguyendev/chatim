package work_test

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	itTimerDelay     = time.Second
	disarmFailMsg    = "member count check timer not disarmed; it will fire and recount"
	itTimerPartition = 2
)

func itTimers(t *testing.T, js jetstream.JetStream, cfg work.StreamConfig, sink *testlog.Sink) *work.Timers {
	t.Helper()
	tm, err := work.NewTimers(js, cfg.Name, cfg.SubjectRoot, cfg.Partitions, itTimerDelay, work.WithTimerLogger(sink.Logger()))
	if err != nil {
		t.Fatalf("NewTimers: %v", err)
	}
	return tm
}

func TestRealJetStreamFiresAnArmedTimerAfterItsDelay(t *testing.T) {
	js, cfg := realWork(t)
	room := roomsOnPartition(itTimerPartition, cfg.Partitions, 1)[0]
	armedAt := time.Now()
	timer, err := itTimers(t, js, cfg, &testlog.Sink{}).Arm(t.Context(), room)
	if err != nil || timer.Seq == 0 {
		t.Fatalf("Arm = %+v, %v; want a stream sequence", timer, err)
	}
	q := work.NewQueue(js, cfg.Name, itTimerPartition, time.Second)
	ds, err := q.Fetch(t.Context(), 1, itTimerDelay+3*time.Second)
	waited := time.Since(armedAt)
	if err != nil || len(ds) != 1 {
		t.Fatalf("Fetch after %v = %d deliveries, %v; want the fired timer", waited, len(ds), err)
	}
	if waited < itTimerDelay {
		t.Fatalf("timer fired after %v, before its %v delay", waited, itTimerDelay)
	}
	r := ds[0].Record()
	if r.Kind != store.MemberCountCheck || r.Room != room || r.User != "" || r.CommittedAt.Before(armedAt.Add(itTimerDelay).Truncate(time.Second)) {
		t.Fatalf("fired record = %+v, want a member count check of room %d stamped with its fire time", r, room)
	}
	if err := ds[0].Ack(); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	awaitEmpty(t, js, cfg.Name)
}

func TestRealJetStreamNeverFiresADisarmedTimer(t *testing.T) {
	js, cfg := realWork(t)
	sink := &testlog.Sink{}
	tm := itTimers(t, js, cfg, sink)
	room := roomsOnPartition(itTimerPartition, cfg.Partitions, 1)[0]
	timer, err := tm.Arm(t.Context(), room)
	if err != nil {
		t.Fatalf("Arm: %v", err)
	}
	tm.Disarm(t.Context(), timer)
	if got := sink.Count(disarmFailMsg); got != 0 {
		t.Fatalf("Disarm logged %d failures, want none", got)
	}
	ds, err := work.NewQueue(js, cfg.Name, itTimerPartition, time.Second).Fetch(t.Context(), 1, itTimerDelay+2*time.Second)
	if err != nil || len(ds) != 0 {
		t.Fatalf("Fetch after a disarm = %d deliveries, %v; want nothing", len(ds), err)
	}
	awaitEmpty(t, js, cfg.Name)
}

func TestEnsureStreamTurnsOnSchedulesOnAnOldWorkStream(t *testing.T) {
	js := itJetStream(t)
	cfg := itWorkConfig(t, js)
	old := jetstream.StreamConfig{
		Name: cfg.Name, Subjects: []string{cfg.SubjectRoot + ".>"}, Retention: jetstream.WorkQueuePolicy,
		Storage: jetstream.FileStorage, Replicas: 1, MaxAge: cfg.MaxAge, Duplicates: cfg.Duplicates,
	}
	if _, err := js.CreateStream(t.Context(), old); err != nil {
		t.Fatalf("create old work stream: %v", err)
	}
	if err := work.EnsureStream(t.Context(), js, cfg); err != nil {
		t.Fatalf("EnsureStream on the old stream: %v", err)
	}
	s, err := js.Stream(t.Context(), cfg.Name)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if info, err := s.Info(t.Context()); err != nil || !info.Config.AllowMsgSchedules || info.Config.Retention != jetstream.WorkQueuePolicy {
		t.Fatalf("stream after ensure = %+v, %v; want a work queue with schedules", info, err)
	}
}
