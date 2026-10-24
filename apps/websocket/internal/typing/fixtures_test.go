package typing

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const testOrigin = "ws-a:0011aabb"

type memberLoader struct {
	mu    sync.Mutex
	rooms map[string][]interest.Room
}

func (l *memberLoader) Load(_ context.Context, tenant, user string) (interest.Membership, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return interest.Membership{Joined: l.rooms[tenant+"/"+user]}, nil
}

type fakeSink struct {
	mu     sync.Mutex
	offers []*chatimv1.TypingNotice
	refuse bool
}

func (s *fakeSink) Send([]byte)         {}
func (s *fakeSink) TrySend([]byte) bool { return true }
func (s *fakeSink) Fail()               {}

func (s *fakeSink) Offer(frame []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refuse {
		return false
	}
	var f chatimv1.ServerFrame
	if err := proto.Unmarshal(frame, &f); err != nil {
		panic(err)
	}
	s.offers = append(s.offers, f.GetTyping())
	return true
}

func (s *fakeSink) notices() []*chatimv1.TypingNotice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*chatimv1.TypingNotice(nil), s.offers...)
}

type published struct {
	subject string
	data    []byte
}

type fakeRelay struct {
	mu           sync.Mutex
	sent         []published
	publishErr   error
	subscribeErr error
	unsubErr     error
	subject      string
	handler      func(subject string, data []byte)
	unsubscribed int
}

func (r *fakeRelay) Publish(subject string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, published{subject: subject, data: data})
	return r.publishErr
}

func (r *fakeRelay) Subscribe(subject string, handler func(string, []byte)) (func() error, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.subscribeErr != nil {
		return nil, r.subscribeErr
	}
	r.subject, r.handler = subject, handler
	return func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.unsubscribed++
		return r.unsubErr
	}, nil
}

func (r *fakeRelay) deliver(subject string, data []byte) {
	r.mu.Lock()
	h := r.handler
	r.mu.Unlock()
	h(subject, data)
}

func (r *fakeRelay) published() []published {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]published(nil), r.sent...)
}

type fixture struct {
	t      *testing.T
	loader *memberLoader
	table  *interest.Table
	svc    *Service
}

func newFixture(t *testing.T, queue int) *fixture {
	t.Helper()
	loader := &memberLoader{rooms: map[string][]interest.Room{}}
	table, err := interest.New(interest.Config{Inflight: 4, OpenPending: 10 * time.Second, ReloadRate: 1000}, loader, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("interest.New: %v", err)
	}
	t.Cleanup(table.Close)
	svc, err := New(Config{Origin: testOrigin, Queue: queue}, table)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &fixture{t: t, loader: loader, table: table, svc: svc}
}

func (f *fixture) member(user string, rooms ...uint64) {
	f.loader.mu.Lock()
	defer f.loader.mu.Unlock()
	for _, room := range rooms {
		f.loader.rooms["t1/"+user] = append(f.loader.rooms["t1/"+user], interest.Room{ID: room, Ver: 1})
	}
}

func (f *fixture) connect(user string, open ...string) (*interest.Conn, *fakeSink) {
	sink := &fakeSink{}
	c := f.table.Register("t1", user, sink)
	synctest.Wait()
	for _, room := range open {
		c.Open(room)
	}
	synctest.Wait()
	return c, sink
}

func (f *fixture) counter(name, reason string) float64 {
	f.t.Helper()
	for _, s := range f.svc.Sources() {
		if s.Name == name && (reason == "" || s.Labels["reason"] == reason) {
			return s.Read()
		}
	}
	f.t.Fatalf("no metric %s %s", name, reason)
	return 0
}

func signal(t *testing.T, sig *chatimv1.TypingSignal) []byte {
	t.Helper()
	raw, err := proto.Marshal(sig)
	if err != nil {
		t.Fatalf("marshal signal: %v", err)
	}
	return raw
}

func typingFrame(room string, stopped bool) *chatimv1.TypingFrame {
	return &chatimv1.TypingFrame{RoomId: room, Stopped: stopped}
}
