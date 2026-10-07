package work_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errAdmin = errors.New("jetstream api down")

type spyAdmin struct {
	streams     []jetstream.StreamConfig
	owners      []string
	consumers   []jetstream.ConsumerConfig
	streamErr   error
	consumerErr error
	failOn      int
}

func (s *spyAdmin) CreateOrUpdateStream(_ context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	s.streams = append(s.streams, cfg)
	return nil, s.streamErr
}

func (s *spyAdmin) CreateOrUpdateConsumer(_ context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	s.owners = append(s.owners, stream)
	s.consumers = append(s.consumers, cfg)
	if s.consumerErr != nil && len(s.consumers) == s.failOn {
		return nil, s.consumerErr
	}
	return nil, nil
}

var valid = work.StreamConfig{
	Name: "CHATIM_WORK", SubjectRoot: "work", Partitions: 3, Replicas: 1,
	MaxAge: 2 * time.Hour, Duplicates: 2 * time.Minute, AckWait: 35 * time.Second,
}

func TestEnsureStreamCreatesAWorkQueueWithSchedulesAndAConsumerPerPartition(t *testing.T) {
	spy := &spyAdmin{}
	if err := work.EnsureStream(t.Context(), spy, valid); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	wantStream := jetstream.StreamConfig{
		Name: "CHATIM_WORK", Subjects: []string{"work.>"}, Retention: jetstream.WorkQueuePolicy,
		Storage: jetstream.FileStorage, Replicas: 1, MaxAge: 2 * time.Hour, Duplicates: 2 * time.Minute,
		AllowMsgSchedules: true,
	}
	if !reflect.DeepEqual(spy.streams, []jetstream.StreamConfig{wantStream}) {
		t.Fatalf("streams = %+v, want %+v", spy.streams, wantStream)
	}
	if len(spy.consumers) != 3 {
		t.Fatalf("consumers = %d, want one per partition", len(spy.consumers))
	}
	for p, got := range spy.consumers {
		want := jetstream.ConsumerConfig{
			Durable: work.ConsumerName(p), FilterSubject: work.Subject("work", p),
			AckPolicy: jetstream.AckExplicitPolicy, AckWait: 35 * time.Second,
			MaxAckPending: work.MaxAckPending, DeliverPolicy: jetstream.DeliverAllPolicy,
		}
		if !reflect.DeepEqual(got, want) || spy.owners[p] != "CHATIM_WORK" {
			t.Fatalf("consumer %d on %s = %+v, want %+v on CHATIM_WORK", p, spy.owners[p], got, want)
		}
	}
}

func TestEnsureStreamSendsTheSameConfigEveryTime(t *testing.T) {
	spy := &spyAdmin{}
	for range 2 {
		if err := work.EnsureStream(t.Context(), spy, valid); err != nil {
			t.Fatalf("EnsureStream: %v", err)
		}
	}
	if len(spy.streams) != 2 || !reflect.DeepEqual(spy.streams[0], spy.streams[1]) || !reflect.DeepEqual(spy.consumers[:3], spy.consumers[3:]) {
		t.Fatalf("second ensure differs: streams %+v consumers %+v", spy.streams, spy.consumers)
	}
}

func TestEnsureStreamNamesTheStepThatFailed(t *testing.T) {
	spy := &spyAdmin{streamErr: errAdmin}
	err := work.EnsureStream(t.Context(), spy, valid)
	if !errors.Is(err, errAdmin) || !strings.Contains(err.Error(), "CHATIM_WORK") || len(spy.consumers) != 0 {
		t.Fatalf("EnsureStream with a failing stream call = %v (consumers %d), want the stream error and no consumer", err, len(spy.consumers))
	}
	spy = &spyAdmin{consumerErr: errAdmin, failOn: 2}
	err = work.EnsureStream(t.Context(), spy, valid)
	if !errors.Is(err, errAdmin) || !strings.Contains(err.Error(), work.ConsumerName(1)) || len(spy.consumers) != 2 {
		t.Fatalf("EnsureStream with a failing consumer = %v (consumers %d), want it to stop at %s", err, len(spy.consumers), work.ConsumerName(1))
	}
}

func TestEnsureStreamRejectsBadConfig(t *testing.T) {
	if err := work.EnsureStream(t.Context(), nil, valid); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("EnsureStream(nil admin) = %v, want ErrInvalidArgument", err)
	}
	tests := []struct {
		name   string
		mutate func(*work.StreamConfig)
	}{
		{"dotted name", func(c *work.StreamConfig) { c.Name = "CHATIM.WORK" }},
		{"wildcard root", func(c *work.StreamConfig) { c.SubjectRoot = "work>" }},
		{"partitions above slot count", func(c *work.StreamConfig) { c.Partitions = 1025 }},
		{"negative partitions", func(c *work.StreamConfig) { c.Partitions = -1 }},
		{"zero replicas", func(c *work.StreamConfig) { c.Replicas = 0 }},
		{"duplicate window above max age", func(c *work.StreamConfig) { c.Duplicates = 3 * time.Hour }},
		{"negative ack wait", func(c *work.StreamConfig) { c.AckWait = -time.Second }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			spy := &spyAdmin{}
			if err := work.EnsureStream(t.Context(), spy, cfg); !errors.Is(err, apperr.ErrInvalidArgument) || len(spy.streams) != 0 {
				t.Fatalf("EnsureStream(%+v) = %v with %d stream calls, want ErrInvalidArgument and none", cfg, err, len(spy.streams))
			}
		})
	}
}

func TestStreamConfigFillsDefaults(t *testing.T) {
	if err := (work.StreamConfig{Name: "W", SubjectRoot: "w", Replicas: 1}).Validate(); err != nil {
		t.Fatalf("Validate with defaults = %v, want nil", err)
	}
	if got := work.ConsumerName(7); got != "work-p7" {
		t.Fatalf("ConsumerName(7) = %q, want work-p7", got)
	}
}
