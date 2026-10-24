package interest

import (
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type noteSink struct {
	mu    sync.Mutex
	notes []*chatimv1.Notification
	other int
}

func (s *noteSink) Send(raw []byte) {
	var f chatimv1.ServerFrame
	if err := proto.Unmarshal(raw, &f); err != nil {
		panic(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := f.GetNotification(); n != nil {
		s.notes = append(s.notes, n)
		return
	}
	s.other++
}

func (s *noteSink) TrySend(raw []byte) bool {
	s.Send(raw)
	return true
}

func (s *noteSink) Offer(raw []byte) bool {
	return s.TrySend(raw)
}

func (s *noteSink) Fail() {}

func (s *noteSink) take() []*chatimv1.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.notes
	s.notes = nil
	return out
}

type publishFixture struct {
	*fixture
	pub   *Publisher
	sinks map[string]*noteSink
}

func newPublishFixture(t *testing.T) *publishFixture {
	f := newFixture(t)
	p := &publishFixture{fixture: f, pub: NewPublisher(f.table), sinks: map[string]*noteSink{}}
	p.join("t1", "u1", "u1")
	p.join("t1", "u2", "u2")
	p.join("t1", "u2", "u2b")
	p.join("t1", "u3", "u3")
	p.join("t2", "u1", "t2u1")
	return p
}

func (p *publishFixture) join(tenant, user, name string) {
	s := &noteSink{}
	mustRegister(p.t, p.table, tenant, user, s)
	synctest.Wait()
	p.sinks[name] = s
}

func (p *publishFixture) receivers() []string {
	var got []string
	for name, s := range p.sinks {
		if len(s.take()) > 0 {
			got = append(got, name)
		}
	}
	slices.Sort(got)
	return got
}

func (p *publishFixture) count(name, reason string) float64 {
	for _, s := range p.pub.Sources() {
		if s.Name == name && s.Labels["reason"] == reason {
			return s.Read()
		}
	}
	p.t.Fatalf("no metric %s{reason=%q}", name, reason)
	return 0
}

func note(id string) *chatimv1.Notification {
	return &chatimv1.Notification{
		Id: id, Tenant: "t1", RoomId: "7", Seq: 3,
		Payload: &chatimv1.Notification_MentionNotified{MentionNotified: &chatimv1.MentionNotified{
			Message: &chatimv1.Message{RoomId: "7", Seq: 3, Text: "hi @u1"},
		}},
	}
}

func packet(tenant, kind string, users ...string) []byte {
	raw, err := proto.Marshal(&chatimv1.WsPublish{
		Id: "n1-p0", Tenant: tenant, Kind: kind, Notification: note("n1"),
		Targets: &chatimv1.Targets{Users: users}, Parts: 1,
	})
	if err != nil {
		panic(err)
	}
	return raw
}

func publishEnv(tenant string, raw []byte) feed.Envelope {
	return feed.Envelope{Tenant: tenant, Data: raw}
}

func TestPublishReachesOnlyTheListedReadyUsersOfTheSubjectTenant(t *testing.T) {
	run(t, func(t *testing.T) {
		p := newPublishFixture(t)
		p.pub.Deliver(publishEnv("t1", packet("t1", "mention", "u1", "u2", "absent")))
		if got, want := p.receivers(), []string{"u1", "u2", "u2b"}; !slices.Equal(got, want) {
			t.Fatalf("receivers = %v, want %v", got, want)
		}
		if got := p.count("publish_delivered_total", ""); got != 3 {
			t.Fatalf("delivered = %v, want 3", got)
		}
		if got := p.count("publish_packets_total", ""); got != 1 {
			t.Fatalf("packets = %v, want 1", got)
		}
	})
}

func TestPublishFrameDecodesToTheSentNotification(t *testing.T) {
	run(t, func(t *testing.T) {
		p := newPublishFixture(t)
		p.pub.Deliver(publishEnv("t1", packet("t1", "mention", "u1")))
		got := p.sinks["u1"].take()
		if len(got) != 1 || !proto.Equal(got[0], note("n1")) {
			t.Fatalf("notifications = %v, want one %v", got, note("n1"))
		}
	})
}

func TestPublishSendsOnceToAUserListedTwice(t *testing.T) {
	run(t, func(t *testing.T) {
		p := newPublishFixture(t)
		p.pub.Deliver(publishEnv("t1", packet("t1", "mention", "u1", "u1")))
		if got := len(p.sinks["u1"].take()); got != 1 {
			t.Fatalf("u1 got %d notifications, want 1", got)
		}
		if got := p.count("publish_delivered_total", ""); got != 1 {
			t.Fatalf("delivered = %v, want 1", got)
		}
	})
}

func TestPublishAcceptsAnUnknownKindToken(t *testing.T) {
	run(t, func(t *testing.T) {
		p := newPublishFixture(t)
		p.pub.Deliver(publishEnv("t1", packet("t1", "never_seen_before", "u3")))
		if got, want := p.receivers(), []string{"u3"}; !slices.Equal(got, want) {
			t.Fatalf("receivers = %v, want %v", got, want)
		}
	})
}

func TestPublishSkipsUsersNotReady(t *testing.T) {
	run(t, func(t *testing.T) {
		p := newPublishFixture(t)
		p.loader.holding(true)
		late := &noteSink{}
		mustRegister(t, p.table, "t1", "late", late)
		synctest.Wait()
		p.pub.Deliver(publishEnv("t1", packet("t1", "mention", "late")))
		if got := len(late.take()); got != 0 {
			t.Fatalf("user still loading got %d notifications, want 0", got)
		}
	})
}

func TestPublishDropsBadPackets(t *testing.T) {
	noNote, err := proto.Marshal(&chatimv1.WsPublish{Tenant: "t1", Kind: "mention", Targets: &chatimv1.Targets{Users: []string{"u1"}}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		env    feed.Envelope
		reason string
	}{
		{"undecodable", publishEnv("t1", []byte{0xff, 0xff, 0xff}), "malformed"},
		{"bad kind", publishEnv("t1", packet("t1", "a.b", "u1")), "malformed"},
		{"empty kind", publishEnv("t1", packet("t1", "", "u1")), "malformed"},
		{"no notification", publishEnv("t1", noNote), "malformed"},
		{"tenant mismatch", publishEnv("t2", packet("t1", "mention", "u1")), "tenant_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run(t, func(t *testing.T) {
				p := newPublishFixture(t)
				p.pub.Deliver(tc.env)
				if got := p.receivers(); len(got) != 0 {
					t.Fatalf("receivers = %v, want none", got)
				}
				if got := p.count("publish_dropped_total", tc.reason); got != 1 {
					t.Fatalf("dropped{%s} = %v, want 1", tc.reason, got)
				}
				if got := p.count("publish_packets_total", ""); got != 1 {
					t.Fatalf("packets = %v, want 1", got)
				}
			})
		})
	}
}
