package publish_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type streamSpy struct {
	got []jetstream.StreamConfig
	err error
}

func (s *streamSpy) CreateOrUpdateStream(_ context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	s.got = append(s.got, cfg)
	return nil, s.err
}

func TestEnsureStreamConfiguresDedupeAndRepublish(t *testing.T) {
	spy := &streamSpy{}
	if err := publish.EnsureStream(t.Context(), spy, publish.StreamConfig{Name: "CHATIM_EVT", SubjectRoot: "evt", LiveRoot: "live", Replicas: 3}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	if len(spy.got) != 1 {
		t.Fatalf("CreateOrUpdateStream called %d times", len(spy.got))
	}
	c := spy.got[0]
	switch {
	case c.Name != "CHATIM_EVT" || len(c.Subjects) != 1 || c.Subjects[0] != "evt.>":
		t.Fatalf("name %q subjects %v", c.Name, c.Subjects)
	case c.Storage != jetstream.FileStorage || c.Replicas != 3:
		t.Fatalf("storage %v replicas %d", c.Storage, c.Replicas)
	case c.MaxAge != 7*24*time.Hour || c.Duplicates != 2*time.Minute:
		t.Fatalf("max age %v duplicates %v", c.MaxAge, c.Duplicates)
	case c.RePublish == nil || c.RePublish.Source != "evt.*.room.*.*" ||
		c.RePublish.Destination != "live.{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}}":
		t.Fatalf("republish %+v", c.RePublish)
	}
	spy.err = errors.New("no jetstream")
	if err := publish.EnsureStream(t.Context(), spy, publish.StreamConfig{Name: "S", SubjectRoot: "evt", LiveRoot: "live", Replicas: 1}); !errors.Is(err, spy.err) {
		t.Fatalf("EnsureStream with a failing server = %v", err)
	}
}

func TestEnsureStreamRejectsBadConfig(t *testing.T) {
	good := publish.StreamConfig{Name: "S", SubjectRoot: "evt", LiveRoot: "live", Replicas: 1}
	for name, mutate := range map[string]func(*publish.StreamConfig){
		"dotted name":     func(c *publish.StreamConfig) { c.Name = "a.b" },
		"wildcard root":   func(c *publish.StreamConfig) { c.SubjectRoot = "evt*" },
		"same roots":      func(c *publish.StreamConfig) { c.LiveRoot = "evt" },
		"zero replicas":   func(c *publish.StreamConfig) { c.Replicas = 0 },
		"dedupe over age": func(c *publish.StreamConfig) { c.MaxAge, c.Duplicates = time.Minute, time.Hour },
	} {
		c := good
		mutate(&c)
		spy := &streamSpy{}
		if err := publish.EnsureStream(t.Context(), spy, c); !errors.Is(err, apperr.ErrInvalidArgument) || len(spy.got) != 0 {
			t.Errorf("%s: EnsureStream = %v after %d calls, want ErrInvalidArgument before any call", name, err, len(spy.got))
		}
	}
	if err := publish.EnsureStream(t.Context(), nil, good); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("EnsureStream(nil) = %v", err)
	}
}

func TestPublishedMessageCarriesSubjectMsgIDAndEvent(t *testing.T) {
	rg := started(t, fastSetup)
	ev := events(roomA, 7)
	want := proto.Clone(ev[0])
	rg.enqueue(t, roomA, 7)
	eventually(t, "event stored", func() bool { return len(rg.js.Stored()) == 1 })
	m := rg.js.Stored()[0]
	if m.Subject != "evt.acme.room.101.msg_created" || publishtest.MsgID(m) != "101-0-7" {
		t.Fatalf("subject %q msg id %q", m.Subject, publishtest.MsgID(m))
	}
	got, err := rg.js.Events()
	if err != nil || len(got) != 1 || !proto.Equal(got[0], want) {
		t.Fatalf("decoded %v (%v), want %v", got, err, want)
	}
}

func TestMalformedEventsAreDroppedAndOthersPublished(t *testing.T) {
	rg := started(t, fastSetup)
	dotted, empty, noID := events(roomA, 2)[0], events(roomA, 3)[0], events(roomA, 5)[0]
	dotted.Tenant, empty.Payload, noID.Id = "acme.x", nil, ""
	if err := rg.Enqueue(roomA, []*chatimv1.Event{dotted, empty, noID, events(roomA, 4)[0]}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	eventually(t, "valid event stored", func() bool { return len(rg.js.Stored()) == 1 })
	if n := rg.sink.Count(malformedMsg); n != 3 {
		t.Fatalf("logged %d malformed events, want 3", n)
	}
	if got := storedIDs(rg.js); got[0] != "101-0-4" {
		t.Fatalf("stored %v, want only 101-0-4", got)
	}
}
