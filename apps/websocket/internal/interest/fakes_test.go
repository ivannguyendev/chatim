package interest

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type reply struct {
	rooms []Room
	left  []Room
	err   error
}

type fakeLoader struct {
	mu      sync.Mutex
	calls   map[userKey]int
	rooms   map[userKey]Membership
	hold    bool
	replies chan reply
	waiting []chan reply
	fail    map[userKey]error
	active  int
	peak    int
	order   []string
}

func newFakeLoader() *fakeLoader {
	return &fakeLoader{calls: map[userKey]int{}, rooms: map[userKey]Membership{}, replies: make(chan reply, 16)}
}

func (l *fakeLoader) Load(ctx context.Context, tenant, user string) (Membership, error) {
	k := userKey{tenant: tenant, user: user}
	l.mu.Lock()
	l.calls[k]++
	l.order = append(l.order, user)
	l.active++
	l.peak = max(l.peak, l.active)
	hold, rooms, failure := l.hold, l.rooms[k], l.fail[k]
	own := make(chan reply, 1)
	l.waiting = append(l.waiting, own)
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.active--
		l.mu.Unlock()
	}()
	if !hold {
		return rooms, failure
	}
	var r reply
	select {
	case r = <-own:
	case r = <-l.replies:
	case <-ctx.Done():
		return Membership{}, ctx.Err()
	}
	return Membership{Joined: r.rooms, Left: r.left}, r.err
}

func (l *fakeLoader) answer(call int, r reply) {
	l.mu.Lock()
	own := l.waiting[call]
	l.mu.Unlock()
	own <- r
}

func (l *fakeLoader) set(tenant, user string, rooms ...uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := make([]Room, len(rooms))
	for i, r := range rooms {
		list[i] = Room{ID: r, Ver: 1}
	}
	k := userKey{tenant: tenant, user: user}
	l.rooms[k] = Membership{Joined: list, Left: l.rooms[k].Left}
}

func (l *fakeLoader) setLeft(tenant, user string, left ...Room) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := userKey{tenant: tenant, user: user}
	l.rooms[k] = Membership{Joined: l.rooms[k].Joined, Left: left}
}

func (l *fakeLoader) failing(tenant, user string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail == nil {
		l.fail = map[userKey]error{}
	}
	l.fail[userKey{tenant: tenant, user: user}] = err
}

func (l *fakeLoader) holding(on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hold = on
}

func (l *fakeLoader) load() (peak, active int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.peak, l.active
}

func (l *fakeLoader) callOrder() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.order)
}

func (l *fakeLoader) callsOf(tenant, user string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls[userKey{tenant: tenant, user: user}]
}

type trySendMode int

const (
	acceptAll trySendMode = iota
	refuseEvents
	refuseAll
)

type fakeSink struct {
	mu       sync.Mutex
	frames   []string
	fails    int
	mode     trySendMode
	sends    int
	trySends int
}

func (s *fakeSink) refusing(mode trySendMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
}

func (s *fakeSink) TrySend(raw []byte) bool {
	line := describe(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trySends++
	if s.mode == refuseAll || (s.mode == refuseEvents && !strings.HasPrefix(line, "resync")) {
		return false
	}
	s.frames = append(s.frames, line)
	return true
}

func (s *fakeSink) Send(raw []byte) {
	line := describe(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends++
	s.frames = append(s.frames, line)
}

func describe(raw []byte) string {
	var f chatimv1.ServerFrame
	if err := proto.Unmarshal(raw, &f); err != nil {
		panic(err)
	}
	var line string
	switch {
	case f.GetReady() != nil:
		line = "ready"
	case f.GetEvent() != nil:
		line = "event " + f.GetEvent().GetId()
	case f.GetRoomClosed() != nil:
		reason := strings.TrimPrefix(f.GetRoomClosed().GetReason().String(), "ROOM_CLOSED_REASON_")
		line = "closed " + f.GetRoomClosed().GetRoomId() + " " + strings.ToLower(reason)
	case f.GetResync() != nil:
		line = "resync " + strings.ToLower(strings.TrimPrefix(f.GetResync().GetReason().String(), "RESYNC_REASON_"))
	default:
		line = "other"
	}
	return line
}

func (s *fakeSink) Fail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails++
}

func (s *fakeSink) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.frames
	s.frames, s.sends, s.trySends = nil, 0, 0
	return out
}

func (s *fakeSink) counts() (sends, trySends int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sends, s.trySends
}

func (s *fakeSink) failures() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fails
}

type fixture struct {
	t      *testing.T
	table  *Table
	loader *fakeLoader
	seq    uint64
}

func newFixture(t *testing.T) *fixture {
	return newFixtureWith(t, Config{Inflight: 64, OpenPending: 10 * time.Second, ReloadRate: 1000})
}

func newFixtureWith(t *testing.T, cfg Config) *fixture {
	t.Helper()
	loader := newFakeLoader()
	table, err := New(cfg, loader, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(table.Close)
	return &fixture{t: t, table: table, loader: loader}
}

func (f *fixture) connect(tenant, user string, rooms ...uint64) (*Conn, *fakeSink) {
	f.t.Helper()
	if len(rooms) > 0 {
		f.loader.set(tenant, user, rooms...)
	}
	sink := &fakeSink{}
	c := f.table.Register(tenant, user, sink)
	synctest.Wait()
	return c, sink
}

func (f *fixture) route(env feed.Envelope) {
	if env.Seq == 0 {
		f.seq++
		env.Seq = f.seq
	}
	f.seq = max(f.seq, env.Seq)
	f.table.Route(env)
}

var dataKinds = map[string]string{
	"msg_created": "message", "msg_edited": "message", "msg_deleted": "message", "reaction_changed": "message", "counts_changed": "message",
	"room_created": "room", "room_updated": "room", "member_count_changed": "room", "msg_pinned": "room", "msg_unpinned": "room",
	"member_added": "member", "member_removed": "member", "member_role_changed": "member", "member_priority_changed": "member",
	"message_hidden": "member", "history_cleared": "member", "bookmark_changed": "member", "room_preferences_changed": "member", "read_updated": "member",
}

func event(kind, tenant string, room uint64, user string, ver uint32) feed.Envelope {
	roomID := strconv.FormatUint(room, 10)
	ev := &chatimv1.Event{Id: fmt.Sprintf("%s/%s/%d/%s/v%d", kind, tenant, room, user, ver), Tenant: tenant, RoomId: roomID}
	switch kind {
	case "member_added":
		ev.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{User: user, Ver: ver}}
	case "member_removed":
		ev.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{User: user, Ver: ver}}
	case "member_role_changed":
		ev.Payload = &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{User: user, Ver: ver}}
	case "message_hidden":
		ev.Payload = &chatimv1.Event_MessageHidden{MessageHidden: &chatimv1.MessageHidden{User: user}}
	case "history_cleared":
		ev.Payload = &chatimv1.Event_HistoryCleared{HistoryCleared: &chatimv1.HistoryCleared{User: user}}
	case "bookmark_changed":
		ev.Payload = &chatimv1.Event_BookmarkChanged{BookmarkChanged: &chatimv1.BookmarkChanged{User: user}}
	case "room_preferences_changed":
		ev.Payload = &chatimv1.Event_RoomPreferencesChanged{RoomPreferencesChanged: &chatimv1.RoomPreferencesChanged{User: user}}
	case "read_updated":
		ev.Payload = &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: user}}
	}
	raw, err := proto.Marshal(ev)
	if err != nil {
		panic(err)
	}
	return feed.Envelope{Tenant: tenant, DataKind: dataKinds[kind], Room: roomID, Kind: kind, ID: ev.GetId(), Data: raw}
}

func msg(tenant string, room uint64) feed.Envelope { return event("msg_created", tenant, room, "", 0) }

func added(tenant string, room uint64, user string, ver uint32) feed.Envelope {
	return event("member_added", tenant, room, user, ver)
}

func removed(tenant string, room uint64, user string, ver uint32) feed.Envelope {
	return event("member_removed", tenant, room, user, ver)
}

func withSeq(env feed.Envelope, seq uint64) feed.Envelope {
	env.Seq = seq
	return env
}

func run(t *testing.T, body func(t *testing.T)) {
	t.Helper()
	synctest.Test(t, body)
}
