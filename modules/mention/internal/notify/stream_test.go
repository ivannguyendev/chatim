package notify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/modules/mention/internal/config"
	"github.com/ivannguyendev/chatim/modules/mention/internal/notify"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func streamConfig() config.Config {
	return config.Config{Enabled: true, EventStream: "CHATIM_EVT", EventSubjectRoot: "evt", LiveRoot: "live", Replicas: 3}
}

func TestEnsureStreamConfig(t *testing.T) {
	admin := &fakeJetStream{}
	noErr(t, notify.EnsureStream(context.Background(), admin, streamConfig()))
	equal(t, jetstream.StreamConfig{
		Name: "CHATIM_NOTIFY", Subjects: []string{"notify.>"},
		Retention: jetstream.LimitsPolicy, Storage: jetstream.FileStorage, Replicas: 3,
		MaxAge: 24 * time.Hour, Duplicates: 6 * time.Minute,
	}, admin.captured)
}

func TestEnsureStreamCustomNames(t *testing.T) {
	cfg := streamConfig()
	cfg.NotifyStream, cfg.NotifySubjectRoot, cfg.NotifyMaxStreamAge, cfg.NotifyDuplicates = "NOTES", "notes", time.Hour, 10*time.Minute
	admin := &fakeJetStream{}
	noErr(t, notify.EnsureStream(context.Background(), admin, cfg))
	equal(t, "NOTES", admin.captured.Name)
	equal(t, []string{"notes.>"}, admin.captured.Subjects)
	equal(t, time.Hour, admin.captured.MaxAge)
	equal(t, 10*time.Minute, admin.captured.Duplicates)
}

func TestEnsureStreamRejects(t *testing.T) {
	errIs(t, notify.EnsureStream(context.Background(), nil, streamConfig()), apperr.ErrInvalidArgument)
	bad := streamConfig()
	bad.Replicas = 0
	errIs(t, notify.EnsureStream(context.Background(), &fakeJetStream{}, bad), apperr.ErrInvalidArgument)
	boom := errors.New("boom")
	errIs(t, notify.EnsureStream(context.Background(), &fakeJetStream{refuse: boom}, streamConfig()), boom)
}
