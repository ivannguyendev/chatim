package publish_test

import (
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealJetStreamUpdatesTheRePublishRuleForDataSubjects(t *testing.T) {
	it := realStream(t, fastSetup)
	stream, err := it.js.Stream(t.Context(), it.cfg.Name)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	old := stream.CachedInfo().Config
	old.RePublish = &jetstream.RePublish{
		Source:      it.cfg.SubjectRoot + ".*.room.*.*",
		Destination: it.cfg.LiveRoot + ".{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}}",
	}
	restored, err := it.js.UpdateStream(t.Context(), old)
	if err != nil {
		t.Fatalf("restore the M2b.3 rule: %v", err)
	}
	if got := restored.CachedInfo().Config.RePublish; got == nil || *got != *old.RePublish {
		t.Fatalf("RePublish after restoring = %+v, want %+v", got, old.RePublish)
	}
	if err := publish.EnsureStream(t.Context(), it.js, it.cfg); err != nil {
		t.Fatalf("EnsureStream over the M2b.3 rule: %v", err)
	}
	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	want := jetstream.RePublish{
		Source:      it.cfg.SubjectRoot + ".*.*.*.*",
		Destination: it.cfg.LiveRoot + ".{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}",
	}
	if got := info.Config.RePublish; got == nil || *got != want {
		t.Fatalf("RePublish after EnsureStream = %+v, want %+v", got, want)
	}

	live, err := it.nc.SubscribeSync(it.cfg.LiveRoot + ".acme.*.101.>")
	if err != nil {
		t.Fatalf("subscribe live: %v", err)
	}
	if err := it.nc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	bob := domain.Member{Room: roomA, Tenant: tenant, User: "bob", Role: domain.RoleMember, State: domain.MemberActive, Ver: 1, UpdatedBy: "alice", UpdatedAt: sentAt}
	for _, ev := range []*chatimv1.Event{events(roomA, 1)[0], roomCreated(roomA), pbconv.MemberEvent(domain.RoomGroup, bob)} {
		msg, err := publish.Message(it.cfg.SubjectRoot, roomA, ev)
		if err != nil {
			t.Fatalf("Message(%s): %v", ev.GetId(), err)
		}
		if _, err := it.js.PublishMsg(t.Context(), msg); err != nil {
			t.Fatalf("PublishMsg(%s): %v", msg.Subject, err)
		}
	}
	var got []string
	for range 3 {
		m, err := live.NextMsg(5 * time.Second)
		if err != nil {
			t.Fatalf("live after %v: %v", got, err)
		}
		got = append(got, m.Subject)
	}
	root := it.cfg.LiveRoot + ".acme."
	wantSubjects := []string{root + "message.101.evt.msg_created", root + "room.101.evt.room_created", root + "member.101.evt.member_added"}
	if !slices.Equal(got, wantSubjects) {
		t.Fatalf("live subjects %v, want %v", got, wantSubjects)
	}
}
