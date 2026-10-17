package mentionapp_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"google.golang.org/grpc"

	"github.com/ivannguyendev/chatim/apps/mention/mentionapp"
)

var errAdmin = errors.New("admin down")

type recordingJS struct {
	jetstream.JetStream
	calls     *[]string
	streamErr error
}

func (r recordingJS) CreateOrUpdateStream(_ context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	*r.calls = append(*r.calls, "stream "+cfg.Name)
	return nil, r.streamErr
}

func (r recordingJS) CreateOrUpdateConsumer(_ context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	*r.calls = append(*r.calls, "consumer "+stream+" "+cfg.Durable)
	return nil, nil
}

func (r recordingJS) Consumer(context.Context, string, string) (jetstream.Consumer, error) {
	return nil, jetstream.ErrConsumerNotFound
}

func newApp(t *testing.T, js recordingJS) *mentionapp.App {
	t.Helper()
	d := deps(t)
	d.JS = js
	app, err := mentionapp.New(config(), d)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return app
}

func bootstrapInto(calls *[]string, err error) func(context.Context, *mongo.Database) error {
	return func(context.Context, *mongo.Database) error {
		*calls = append(*calls, "bootstrap")
		return err
	}
}

func TestPrepareBootstrapsThenEnsuresTheNotifyStreamAndTheConsumer(t *testing.T) {
	var calls []string
	app := newApp(t, recordingJS{calls: &calls})
	mentionapp.SetBootstrap(app, bootstrapInto(&calls, nil))
	if err := app.Prepare(t.Context()); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	want := []string{"bootstrap", "stream CHATIM_NOTIFY", "consumer CHATIM_EVT mention"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestPrepareStopsAtTheFirstFailure(t *testing.T) {
	var calls []string
	app := newApp(t, recordingJS{calls: &calls})
	mentionapp.SetBootstrap(app, bootstrapInto(&calls, errAdmin))
	if err := app.Prepare(t.Context()); !errors.Is(err, errAdmin) || !slices.Equal(calls, []string{"bootstrap"}) {
		t.Fatalf("prepare = %v after %v, want the bootstrap error alone", err, calls)
	}
	calls = nil
	app = newApp(t, recordingJS{calls: &calls, streamErr: errAdmin})
	mentionapp.SetBootstrap(app, bootstrapInto(&calls, nil))
	if err := app.Prepare(t.Context()); !errors.Is(err, errAdmin) || len(calls) != 2 {
		t.Fatalf("prepare = %v after %v, want the stream error before the consumer", err, calls)
	}
}

func TestRunStopsCleanlyOnClose(t *testing.T) {
	var calls []string
	app := newApp(t, recordingJS{calls: &calls})
	done := make(chan error, 1)
	go func() { done <- app.Run(t.Context()) }()
	if err := app.Close(t.Context()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("run = %v, want nil after close", err)
	}
}

func TestRegisterAddsTheMentionService(t *testing.T) {
	var calls []string
	app := newApp(t, recordingJS{calls: &calls})
	srv := grpc.NewServer()
	app.Register(srv)
	if _, ok := srv.GetServiceInfo()["chatim.v1.MentionService"]; !ok {
		t.Fatalf("services = %v, want chatim.v1.MentionService", srv.GetServiceInfo())
	}
	if app.Watch() == nil {
		t.Fatal("watch = nil")
	}
}

func TestProbesNameEveryMentionMetric(t *testing.T) {
	var calls []string
	app := newApp(t, recordingJS{calls: &calls})
	var names []string
	for _, p := range app.Probes() {
		if p.Read() != 0 || p.Gauge != (p.Name == "lag_seconds") {
			t.Fatalf("probe %s reads %v gauge %v on a fresh app", p.Name, p.Read(), p.Gauge)
		}
		names = append(names, p.Name+p.Labels["reason"])
	}
	want := []string{
		"lag_seconds", "failures_total", "dropped_total", "stuck_total", "notified_total", "retracted_total", "notify_failures_total",
		"room_groups_hits_total", "room_groups_misses_total", "room_groups_rebuild_failures_total",
		"room_groups_watch_forgets_total", "room_groups_watch_malformed_total", "notify_skipped_totalstale", "notify_skipped_totalsuperseded",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("probes = %v, want %v", names, want)
	}
}
