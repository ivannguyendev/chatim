package timer_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type fakeAdmin struct {
	streams   []jetstream.StreamConfig
	consumers []jetstream.ConsumerConfig
	on        []string
	err       error
}

func (f *fakeAdmin) CreateOrUpdateStream(_ context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	f.streams = append(f.streams, cfg)
	return nil, f.err
}

func (f *fakeAdmin) CreateOrUpdateConsumer(_ context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	f.consumers, f.on = append(f.consumers, cfg), append(f.on, stream)
	return nil, nil
}

var timerStream = timer.StreamConfig{Name: "CHATIM_TIMER", SubjectRoot: "timer", Replicas: 3}

func TestEnsureStreamMakesAScheduledWorkQueueWithOneSharedConsumer(t *testing.T) {
	js := &fakeAdmin{}
	if err := timer.EnsureStream(t.Context(), js, timerStream); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	if len(js.streams) != 1 || len(js.consumers) != 1 {
		t.Fatalf("made %d streams and %d consumers, want one of each", len(js.streams), len(js.consumers))
	}
	s := js.streams[0]
	if s.Name != "CHATIM_TIMER" || !slices.Equal(s.Subjects, []string{"timer.arm.>", "timer.due"}) || s.Retention != jetstream.WorkQueuePolicy ||
		s.Storage != jetstream.FileStorage || s.Replicas != 3 || s.MaxAge != 2*time.Hour || s.Duplicates != 2*time.Minute || !s.AllowMsgSchedules {
		t.Fatalf("stream = %+v", s)
	}
	c := js.consumers[0]
	if js.on[0] != "CHATIM_TIMER" || c.Durable != timer.ConsumerName || c.FilterSubject != "timer.due" || c.AckPolicy != jetstream.AckExplicitPolicy ||
		c.AckWait != 30*time.Second || c.MaxAckPending != timer.MaxAckPending {
		t.Fatalf("consumer on %s = %+v", js.on[0], c)
	}
}

func TestEnsureStreamRefusesBadConfig(t *testing.T) {
	bad := map[string]timer.StreamConfig{
		"dotted name":        {Name: "A.B", SubjectRoot: "timer", Replicas: 1},
		"wildcard root":      {Name: "T", SubjectRoot: "ti*", Replicas: 1},
		"no replicas":        {Name: "T", SubjectRoot: "timer"},
		"dedupe beyond life": {Name: "T", SubjectRoot: "timer", Replicas: 1, MaxAge: time.Minute, Duplicates: time.Hour},
	}
	for name, cfg := range bad {
		if err := timer.EnsureStream(t.Context(), &fakeAdmin{}, cfg); !errors.Is(err, apperr.ErrInvalidArgument) || !errors.Is(cfg.Validate(), apperr.ErrInvalidArgument) {
			t.Errorf("%s: EnsureStream = %v, want ErrInvalidArgument", name, err)
		}
	}
	if err := timer.EnsureStream(t.Context(), nil, timerStream); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("EnsureStream without a client = %v", err)
	}
	down := errors.New("nats down")
	if err := timer.EnsureStream(t.Context(), &fakeAdmin{err: down}, timerStream); !errors.Is(err, down) {
		t.Errorf("EnsureStream with NATS down = %v", err)
	}
}
