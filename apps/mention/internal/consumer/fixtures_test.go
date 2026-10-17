package consumer_test

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/mention/internal/config"
	"github.com/ivannguyendev/chatim/apps/mention/internal/consumer"
	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const tenant = "t1"

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func cfg() config.Config {
	c := config.Defaults()
	c.EventStream, c.EventSubjectRoot, c.LiveRoot, c.Replicas = "CHATIM_EVT", "evt", "live", 1
	return c
}

func user(id string, at time.Time) *chatimv1.MentionTarget {
	return &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: id, At: timestamppb.New(at)}
}

func group(id string, at time.Time) *chatimv1.MentionTarget {
	return &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: id, At: timestamppb.New(at)}
}

func message(seq uint64, ver uint32, sent time.Time, targets ...*chatimv1.MentionTarget) *chatimv1.Message {
	return &chatimv1.Message{RoomId: "777", Seq: seq, Sender: "Lan", CreatedAt: timestamppb.New(sent), Ver: ver, MentionTargets: targets}
}

func envelope(seq uint64, at time.Time) *chatimv1.Event {
	return &chatimv1.Event{Tenant: tenant, RoomId: "777", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP, Seq: seq, Actor: "Lan", Ts: timestamppb.New(at)}
}

func created(seq uint64, at time.Time, targets ...*chatimv1.MentionTarget) *chatimv1.Event {
	ev := envelope(seq, at)
	ev.Payload = &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{Message: message(seq, 0, at, targets...)}}
	return ev
}

func edited(seq uint64, ver uint32, at time.Time, current []*chatimv1.MentionTarget, previous ...*chatimv1.MentionTarget) *chatimv1.Event {
	ev := envelope(seq, at)
	ev.Payload = &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{
		Message: message(seq, ver, at, current...), Ver: ver, PreviousMentions: &chatimv1.MentionSet{Targets: previous},
	}}
	return ev
}

func key(seq uint64) domain.MessageKey { return domain.MessageKey{Room: 777, Seq: seq} }

type rig struct {
	mentions *countingMentions
	rooms    *memstore.Rooms
	versions *fakeVersions
	resolver *fakeResolver
	js       *fakeJetStream
}

func newRig() *rig {
	return &rig{
		mentions: &countingMentions{Mentions: memstore.NewMentions(), applied: map[domain.MessageKey]int{}},
		rooms:    memstore.NewRooms(),
		versions: &fakeVersions{current: map[domain.MessageKey]uint32{}},
		resolver: &fakeResolver{groups: map[string][]string{}},
		js:       &fakeJetStream{},
	}
}

func (r *rig) deps(src jsconsume.Source) consumer.Deps {
	return consumer.Deps{
		Source: src, Mentions: r.mentions, Rooms: r.rooms, Versions: r.versions,
		Resolver: r.resolver, Notify: r.js, Log: slog.New(slog.DiscardHandler),
	}
}

func (r *rig) consumer(t *testing.T, src jsconsume.Source, c config.Config) *consumer.Consumer {
	t.Helper()
	out, err := consumer.New(c, r.deps(src))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return out
}

type running struct {
	c    *consumer.Consumer
	done chan error
}

func start(ctx context.Context, c *consumer.Consumer) running {
	r := running{c: c, done: make(chan error, 1)}
	go func() { r.done <- c.Run(ctx) }()
	return r
}

func (r running) stop(t *testing.T) {
	t.Helper()
	if err := r.c.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-r.done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func runOnce(t *testing.T, r *rig, c config.Config, ds ...*fakeDelivery) *consumer.Consumer {
	t.Helper()
	q := &fakeQueue{}
	q.push(ds...)
	cons := r.consumer(t, q, c)
	run := start(t.Context(), cons)
	synctest.Wait()
	run.stop(t)
	return cons
}
