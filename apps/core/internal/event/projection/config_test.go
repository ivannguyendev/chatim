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
	for name, c := range map[string]projection.Config{
		"no stream":        {SubjectRoot: subjectRoot},
		"dotted root":      {Stream: "CHATIM_EVT", SubjectRoot: "evt.x"},
		"negative batch":   {Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchBatch: -1},
		"batch over limit": {Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchBatch: projection.MaxAckPending + 1},
		"negative wait":    {Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchWait: -time.Second},
	} {
		if err := c.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: Validate = %v, want ErrInvalidArgument", name, err)
		}
	}
}

type fakeAdmin struct {
	stream string
	cfg    jetstream.ConsumerConfig
	err    error
}

func (a *fakeAdmin) CreateOrUpdateConsumer(_ context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	a.stream, a.cfg = stream, cfg
	return nil, a.err
}

func TestEnsureConsumerMakesASharedDurableFilteredToTheProjection(t *testing.T) {
	admin := &fakeAdmin{}
	p := projection.NewRoomActivity(&fakeWriter{})
	if err := projection.EnsureConsumer(t.Context(), admin, evtConfig, p); err != nil {
		t.Fatalf("EnsureConsumer: %v", err)
	}
	c := admin.cfg
	if admin.stream != "CHATIM_EVT" || c.Durable != projection.DurableName(projection.RoomActivityName) || c.Durable != "projection-room_activity" {
		t.Errorf("consumer %q on %q, want projection-room_activity on CHATIM_EVT", c.Durable, admin.stream)
	}
	if !slices.Equal(c.FilterSubjects, p.Subjects(subjectRoot)) || c.FilterSubject != "" {
		t.Errorf("filters %v / %q, want the projection subjects %v", c.FilterSubjects, c.FilterSubject, p.Subjects(subjectRoot))
	}
	if c.AckPolicy != jetstream.AckExplicitPolicy || c.AckWait != 30*time.Second || c.DeliverPolicy != jetstream.DeliverAllPolicy || c.MaxAckPending != 4096 {
		t.Errorf("consumer config %+v; want explicit ack, 30s ack wait, deliver all, 4096 pending", c)
	}
}

func TestEnsureConsumerRefusesBadInputAndReportsTheServer(t *testing.T) {
	p := projection.NewRoomActivity(&fakeWriter{})
	if err := projection.EnsureConsumer(t.Context(), nil, evtConfig, p); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("nil client: %v", err)
	}
	if err := projection.EnsureConsumer(t.Context(), &fakeAdmin{}, projection.Config{}, p); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("empty config: %v", err)
	}
	errDown := errors.New("nats down")
	if err := projection.EnsureConsumer(t.Context(), &fakeAdmin{err: errDown}, evtConfig, p); !errors.Is(err, errDown) {
		t.Errorf("server error: %v", err)
	}
}
