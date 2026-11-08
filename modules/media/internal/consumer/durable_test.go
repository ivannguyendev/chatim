package consumer_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/modules/media/internal/consumer"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

type fakeAdmin struct {
	stream string
	got    jetstream.ConsumerConfig
	err    error
}

func (a *fakeAdmin) CreateOrUpdateConsumer(_ context.Context, stream string, c jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	a.stream, a.got = stream, c
	return nil, a.err
}

func TestEnsureConsumerConfig(t *testing.T) {
	a := &fakeAdmin{}
	if err := consumer.EnsureConsumer(t.Context(), a, cfg()); err != nil {
		t.Fatalf("EnsureConsumer: %v", err)
	}
	want := []string{"evt.*.message.*.msg_created", "evt.*.mention.*.msg_created", "evt.*.message.*.msg_deleted"}
	got := a.got
	if a.stream != "CHATIM_EVT" || got.Durable != "media" || !slices.Equal(got.FilterSubjects, want) || got.FilterSubject != "" {
		t.Errorf("stream %q config %+v", a.stream, got)
	}
	if got.AckPolicy != jetstream.AckExplicitPolicy || got.AckWait != 30*time.Second || got.DeliverPolicy != jetstream.DeliverAllPolicy {
		t.Errorf("config %+v", got)
	}
	if consumer.StopTimeout() != 1250*time.Millisecond {
		t.Errorf("stop timeout %v", consumer.StopTimeout())
	}
}

func TestEnsureConsumerRejects(t *testing.T) {
	if err := consumer.EnsureConsumer(t.Context(), nil, cfg()); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("nil admin = %v", err)
	}
	bad := cfg()
	bad.Stream = ""
	if err := consumer.EnsureConsumer(t.Context(), &fakeAdmin{}, bad); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("bad config = %v", err)
	}
	if err := consumer.EnsureConsumer(t.Context(), &fakeAdmin{err: errDown}, cfg()); !errors.Is(err, errDown) {
		t.Errorf("admin error = %v", err)
	}
}

func TestNewRejectsMissingDepsAndBadConfig(t *testing.T) {
	for name, drop := range map[string]func(*consumer.Deps){
		"source": func(d *consumer.Deps) { d.Source = nil },
		"sync":   func(d *consumer.Deps) { d.Sync = nil },
		"stats":  func(d *consumer.Deps) { d.Stats = nil },
		"ready":  func(d *consumer.Deps) { d.Ready = nil },
	} {
		d := newRig().deps(&fakeQueue{})
		drop(&d)
		if _, err := consumer.New(cfg(), d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v", name, err)
		}
	}
	for name, change := range map[string]func(*consumer.Config){
		"workers":  func(c *consumer.Config) { c.Workers = 0 },
		"batch":    func(c *consumer.Config) { c.FetchBatch = 0 },
		"ack wait": func(c *consumer.Config) { c.AckWait = time.Second },
		"timeout":  func(c *consumer.Config) { c.SyncTimeout = 0 },
		"root":     func(c *consumer.Config) { c.SubjectRoot = "" },
	} {
		bad := cfg()
		change(&bad)
		if _, err := consumer.New(bad, newRig().deps(&fakeQueue{})); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v", name, err)
		}
	}
}
