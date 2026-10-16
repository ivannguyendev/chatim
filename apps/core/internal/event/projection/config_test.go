package projection_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestConfigFillsDefaultsAndRefusesBadValues(t *testing.T) {
	if err := evtConfig.Validate(); err != nil {
		t.Fatalf("default config: %v", err)
	}
	if projection.DefaultFetchBatch != 1024 || projection.DefaultFetchWait != time.Second || evtConfig.MaxAckPending() != 4096 {
		t.Fatalf("defaults %d / %v / %d pending, want 1024 / 1s / 4096", projection.DefaultFetchBatch, projection.DefaultFetchWait, evtConfig.MaxAckPending())
	}
	full := projection.Config{Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchBatch: projection.MaxFetchBatch, FetchWait: projection.MaxFetchWait}
	if err := full.Validate(); err != nil || full.MaxAckPending() != 4*projection.MaxFetchBatch {
		t.Fatalf("config at the limits: %v, %d pending", err, full.MaxAckPending())
	}
	for name, c := range map[string]projection.Config{
		"no stream":                {SubjectRoot: subjectRoot},
		"dotted root":              {Stream: "CHATIM_EVT", SubjectRoot: "evt.x"},
		"negative batch":           {Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchBatch: -1},
		"batch over limit":         {Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchBatch: projection.MaxFetchBatch + 1},
		"wait past the stop phase": {Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchWait: projection.MaxFetchWait + time.Millisecond},
		"negative wait":            {Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchWait: -time.Second},
	} {
		if err := c.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: Validate = %v, want ErrInvalidArgument", name, err)
		}
	}
}

type fakeAdmin struct {
	stream    string
	cfg       jetstream.ConsumerConfig
	err       error
	deleted   []string
	deleteErr map[string]error
}

func (a *fakeAdmin) CreateOrUpdateConsumer(_ context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	a.stream, a.cfg = stream, cfg
	return nil, a.err
}

func (a *fakeAdmin) DeleteConsumer(_ context.Context, _, name string) error {
	a.deleted = append(a.deleted, name)
	if err, ok := a.deleteErr[name]; ok {
		return err
	}
	return jetstream.ErrConsumerNotFound
}

func TestEnsureConsumerMakesOneSharedDurableWithExplicitAcks(t *testing.T) {
	admin := &fakeAdmin{deleteErr: map[string]error{"projection-mention_read": nil}}
	tuned := projection.Config{Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchBatch: 300}
	if err := projection.EnsureConsumer(t.Context(), admin, tuned); err != nil {
		t.Fatalf("EnsureConsumer: %v", err)
	}
	c := admin.cfg
	if admin.stream != "CHATIM_EVT" || c.Durable != "projections" {
		t.Errorf("consumer %q on %q, want projections on CHATIM_EVT", c.Durable, admin.stream)
	}
	if c.FilterSubject != "evt.>" || len(c.FilterSubjects) != 0 {
		t.Errorf("filters %q / %v, want the single subject evt.>", c.FilterSubject, c.FilterSubjects)
	}
	if c.AckPolicy != jetstream.AckExplicitPolicy || c.AckWait != 30*time.Second || c.DeliverPolicy != jetstream.DeliverAllPolicy || c.MaxAckPending != 1200 {
		t.Errorf("consumer config %+v; want explicit ack, 30s ack wait, deliver all, four batches pending", c)
	}
	want := []string{"projection-room_activity", "projection-mention_reply_index", "projection-mention_read"}
	if !slices.Equal(admin.deleted, want) {
		t.Errorf("deleted %v, want the three old durables (missing ones ignored)", admin.deleted)
	}
}

func TestEnsureConsumerRefusesBadInputAndReportsTheServer(t *testing.T) {
	if err := projection.EnsureConsumer(t.Context(), nil, evtConfig); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("nil client: %v", err)
	}
	if err := projection.EnsureConsumer(t.Context(), &fakeAdmin{}, projection.Config{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("empty config: %v", err)
	}
	errDown := errors.New("nats down")
	if err := projection.EnsureConsumer(t.Context(), &fakeAdmin{err: errDown}, evtConfig); !errors.Is(err, errDown) {
		t.Errorf("server error: %v", err)
	}
	refused := &fakeAdmin{deleteErr: map[string]error{"projection-room_activity": errDown}}
	if err := projection.EnsureConsumer(t.Context(), refused, evtConfig); !errors.Is(err, errDown) {
		t.Errorf("refused delete: %v", err)
	}
}
