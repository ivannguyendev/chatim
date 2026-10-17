package consumer_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/mention/internal/consumer"
	"github.com/ivannguyendev/chatim/pkg/apperr"
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
	c := cfg()
	c.MaxAckPending = 4096
	if err := consumer.EnsureConsumer(t.Context(), a, c); err != nil {
		t.Fatalf("EnsureConsumer: %v", err)
	}
	want := []string{"evt.*.mention.*.msg_created", "evt.*.message.*.msg_edited", "evt.*.message.*.msg_deleted"}
	got := a.got
	if a.stream != "CHATIM_EVT" || got.Durable != "mention" || !slices.Equal(got.FilterSubjects, want) || got.FilterSubject != "" {
		t.Errorf("stream %q config %+v", a.stream, got)
	}
	if got.AckPolicy != jetstream.AckExplicitPolicy || got.AckWait != consumer.AckWait || consumer.AckWait.Seconds() != 30 ||
		got.MaxAckPending != 4096 || got.DeliverPolicy != jetstream.DeliverAllPolicy {
		t.Errorf("config %+v", got)
	}
	if def := consumer.ConsumerConfig(cfg()); def.MaxAckPending != 16384 {
		t.Errorf("default max ack pending %d, want 16384", def.MaxAckPending)
	}
}

func TestEnsureConsumerRejects(t *testing.T) {
	if err := consumer.EnsureConsumer(t.Context(), nil, cfg()); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("nil admin = %v", err)
	}
	bad := cfg()
	bad.EventStream = "a.b"
	if err := consumer.EnsureConsumer(t.Context(), &fakeAdmin{}, bad); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("bad config = %v", err)
	}
	if err := consumer.EnsureConsumer(t.Context(), &fakeAdmin{err: errDown}, cfg()); !errors.Is(err, errDown) {
		t.Errorf("admin error = %v", err)
	}
}

func TestNewRejectsMissingDepsAndBadConfig(t *testing.T) {
	for name, drop := range map[string]func(*consumer.Deps){
		"source":   func(d *consumer.Deps) { d.Source = nil },
		"mentions": func(d *consumer.Deps) { d.Mentions = nil },
		"rooms":    func(d *consumer.Deps) { d.Rooms = nil },
		"versions": func(d *consumer.Deps) { d.Versions = nil },
		"resolver": func(d *consumer.Deps) { d.Resolver = nil },
		"notify":   func(d *consumer.Deps) { d.Notify = nil },
	} {
		d := newRig().deps(&fakeQueue{})
		drop(&d)
		if _, err := consumer.New(cfg(), d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v", name, err)
		}
	}
	bad := cfg()
	bad.Workers = -1
	if _, err := consumer.New(bad, newRig().deps(&fakeQueue{})); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("bad config: New = %v", err)
	}
}
