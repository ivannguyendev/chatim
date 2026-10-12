package work_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const itNATSURLEnv = "CHATIM_IT_NATS_URL"

func itJetStream(t *testing.T) jetstream.JetStream {
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
	return js
}

func itWorkConfig(t *testing.T, js jetstream.JetStream) work.StreamConfig {
	t.Helper()
	var b [6]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])
	cfg := work.StreamConfig{
		Name: "IT_WORK_" + strings.ToUpper(suffix), SubjectRoot: "itwork" + suffix, Partitions: 4, Replicas: 1,
		MaxAge: time.Hour, Duplicates: time.Minute, AckWait: 30 * time.Second,
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := js.DeleteStream(ctx, cfg.Name); err != nil {
			t.Errorf("delete stream %s: %v", cfg.Name, err)
		}
	})
	return cfg
}

func realWork(t *testing.T) (jetstream.JetStream, work.StreamConfig) {
	t.Helper()
	js := itJetStream(t)
	cfg := itWorkConfig(t, js)
	for range 2 {
		if err := work.EnsureStream(t.Context(), js, cfg); err != nil {
			t.Fatalf("EnsureStream: %v", err)
		}
	}
	return js, cfg
}

func roomsOnPartition(p, partitions, n int) []uint64 {
	var out []uint64
	for r := uint64(1); len(out) < n; r++ {
		if work.Partition(r, partitions) == p {
			out = append(out, r)
		}
	}
	return out
}

func TestRealWorkStreamIsAWorkQueueWithAConsumerPerPartition(t *testing.T) {
	js, cfg := realWork(t)
	s, err := js.Stream(t.Context(), cfg.Name)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := s.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if info.Config.Retention != jetstream.WorkQueuePolicy || info.Config.Duplicates != time.Minute {
		t.Fatalf("stream config = %+v, want a work queue with a 1m duplicate window", info.Config)
	}
	for p := range cfg.Partitions {
		c, err := js.Consumer(t.Context(), cfg.Name, work.ConsumerName(p))
		if err != nil {
			t.Fatalf("consumer %d: %v", p, err)
		}
		ci := c.CachedInfo()
		if ci.Config.FilterSubject != work.Subject(cfg.SubjectRoot, p) || ci.Config.AckPolicy != jetstream.AckExplicitPolicy || ci.Config.MaxAckPending != work.MaxAckPending {
			t.Fatalf("consumer %d config = %+v", p, ci.Config)
		}
	}
}

func TestRealWorkQueueFetchesAcksAndRedeliversNaks(t *testing.T) {
	js, cfg := realWork(t)
	committed := time.Now().UTC()
	var want []work.Record
	for _, room := range roomsOnPartition(1, cfg.Partitions, 3) {
		r := work.Record{Kind: store.MessageInserted, Room: room, Seq: 1, CommittedAt: committed}
		if _, err := js.PublishMsg(t.Context(), work.Message(cfg.SubjectRoot, cfg.Partitions, r)); err != nil {
			t.Fatalf("publish %s: %v", r.ID(), err)
		}
		want = append(want, r)
	}
	q := work.NewQueue(js, cfg.Name, 1, time.Second)
	ds, err := q.Fetch(t.Context(), 10, 2*time.Second)
	if err != nil || len(ds) != 3 {
		t.Fatalf("Fetch = %d deliveries, %v; want 3", len(ds), err)
	}
	for i, d := range ds {
		if got := d.Record(); got.ID() != want[i].ID() || !got.CommittedAt.Equal(committed) {
			t.Fatalf("delivery %d = %+v, want %+v", i, got, want[i])
		}
	}
	for _, d := range ds[:2] {
		if err := d.Ack(); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	nakAt := time.Now()
	if err := ds[2].Nak(500 * time.Millisecond); err != nil {
		t.Fatalf("Nak: %v", err)
	}
	again, err := q.Fetch(t.Context(), 10, 3*time.Second)
	if err != nil || len(again) != 1 || again[0].Record().ID() != want[2].ID() || time.Since(nakAt) < 300*time.Millisecond {
		t.Fatalf("redelivery = %d deliveries, %v after %v; want %s after the nak delay", len(again), err, time.Since(nakAt), want[2].ID())
	}
	if err := again[0].Ack(); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	awaitEmpty(t, js, cfg.Name)
}

func TestRealWorkQueueTerminatesUndecodableRecords(t *testing.T) {
	js, cfg := realWork(t)
	junk := &nats.Msg{Subject: work.Subject(cfg.SubjectRoot, 2), Data: []byte("junk"), Header: nats.Header{}}
	junk.Header.Set(jetstream.MsgIDHeader, "junk-1")
	if _, err := js.PublishMsg(t.Context(), junk); err != nil {
		t.Fatalf("publish junk: %v", err)
	}
	ds, err := work.NewQueue(js, cfg.Name, 2, time.Second).Fetch(t.Context(), 10, 2*time.Second)
	var bad work.BadRecordsError
	if len(ds) != 0 || !errors.As(err, &bad) || bad.Terminated != 1 || !errors.Is(err, work.ErrBadRecord) {
		t.Fatalf("Fetch of junk = %d deliveries, %v; want none and one terminated bad record", len(ds), err)
	}
	awaitEmpty(t, js, cfg.Name)
}

func awaitEmpty(t *testing.T, js jetstream.JetStream, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, err := js.Stream(t.Context(), name)
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		info, err := s.Info(t.Context())
		if err != nil {
			t.Fatalf("stream info: %v", err)
		}
		if info.State.Msgs == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("work stream still holds %d messages 5s after every ack", info.State.Msgs)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
