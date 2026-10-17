package timer_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

const (
	itNATSURLEnv  = "CHATIM_IT_NATS_URL"
	itTimerDelay  = time.Second
	itFireLimit   = itTimerDelay + 5*time.Second
	disarmFailMsg = "count check timer not disarmed; it will fire and recount"
)

func realTimerStream(t *testing.T) (jetstream.JetStream, timer.StreamConfig) {
	t.Helper()
	url := os.Getenv(itNATSURLEnv)
	if url == "" {
		t.Skip("set CHATIM_IT_NATS_URL to run")
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect %s: %v", url, err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])
	cfg := timer.StreamConfig{Name: "IT_TIMER_" + strings.ToUpper(suffix), SubjectRoot: "ittimer" + suffix, Replicas: 1}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := js.DeleteStream(ctx, cfg.Name); err != nil {
			t.Errorf("delete stream %s: %v", cfg.Name, err)
		}
	})
	for range 2 {
		if err := timer.EnsureStream(t.Context(), js, cfg); err != nil {
			t.Fatalf("EnsureStream: %v", err)
		}
	}
	return js, cfg
}

func itTimers(t *testing.T, js jetstream.JetStream, cfg timer.StreamConfig, sink *testlog.Sink) *timer.Timers {
	t.Helper()
	return built(timer.NewTimers(js, cfg.Name, cfg.SubjectRoot, itTimerDelay, timer.WithLogger(sink.Logger())))(t)
}

func streamMsgs(t *testing.T, js jetstream.JetStream, name string) uint64 {
	t.Helper()
	s, err := js.Stream(t.Context(), name)
	if err != nil {
		t.Fatalf("stream %s: %v", name, err)
	}
	info, err := s.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	return info.State.Msgs
}

func TestRealJetStreamNeverFiresADisarmedTimer(t *testing.T) {
	js, cfg := realTimerStream(t)
	sink := &testlog.Sink{}
	tm := itTimers(t, js, cfg, sink)
	armed, err := tm.ArmMemberCountCheck(t.Context(), room)
	if err != nil || armed.Seq == 0 {
		t.Fatalf("ArmMemberCountCheck = %+v, %v", armed, err)
	}
	tm.Disarm(t.Context(), armed)
	if got := sink.Count(disarmFailMsg); got != 0 {
		t.Fatalf("Disarm logged %d failures, want none", got)
	}
	c, err := js.Consumer(t.Context(), cfg.Name, timer.ConsumerName)
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	batch, err := c.Fetch(1, jetstream.FetchMaxWait(itTimerDelay+2*time.Second))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	for m := range batch.Messages() {
		t.Fatalf("a disarmed timer fired on %s", m.Subject())
	}
	if n := streamMsgs(t, js, cfg.Name); n != 0 {
		t.Fatalf("stream holds %d messages after a disarm, want 0", n)
	}
}

func TestRealJetStreamFiresAnArmedTimerAndTheConsumerRepairs(t *testing.T) {
	js, cfg := realTimerStream(t)
	rooms, events := memstore.NewRooms(), &fakeEvents{}
	r := createRoom(t, rooms, room)
	if _, err := rooms.AddMembers(t.Context(), domain.Join{Room: room, Tenant: tenant, RequestID: "req-1", By: "alice", At: repairedAt}, []string{"bob"}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	tm := itTimers(t, js, cfg, &testlog.Sink{})
	members := built(timer.NewMemberCountRepair(timer.MemberCountRepairDeps{Rooms: rooms, Counts: rooms, Timers: tm, Events: events}))(t)
	messages := &fakeRepairer{}
	c := built(timer.NewConsumer(timer.ConsumerDeps{Source: jsconsume.NewPullSource(js, cfg.Name, timer.ConsumerName), Members: members, Messages: messages}, timer.ConsumerConfig{}, nil))(t)
	armedAt := time.Now()
	if _, err := tm.ArmMemberCountCheck(t.Context(), room); err != nil {
		t.Fatalf("ArmMemberCountCheck: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Run(context.WithoutCancel(t.Context())) }()
	t.Cleanup(func() {
		if err := c.Close(context.Background()); err != nil {
			t.Errorf("Close: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Run = %v", err)
		}
	})
	await(t, "the repair", func() bool { return members.Repaired() == 1 })
	if waited := time.Since(armedAt); waited < itTimerDelay {
		t.Fatalf("repaired after %v, before the %v delay", waited, itTimerDelay)
	}
	if got, want := events.ids(), pbconv.MemberCountEventID(room, r.MemberCountVer+1); len(got) != 1 || got[0] != want {
		t.Fatalf("events = %v, want %s", got, want)
	}
	await(t, "only the follow-up timer left", func() bool { return streamMsgs(t, js, cfg.Name) == 1 })
}

func await(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(itFireLimit)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %v", what, itFireLimit)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
