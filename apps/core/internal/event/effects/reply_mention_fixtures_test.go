package effects_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

var (
	indexedAt   = time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	mentionMinh = domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"}
	mentionAll  = domain.MentionTarget{Kind: domain.MentionAll}
)

type linkTimers struct {
	fakeMessageTimers
	disarmMu sync.Mutex
	disarmed []uint64
}

func (l *linkTimers) Disarm(_ context.Context, tm work.Timer) {
	l.disarmMu.Lock()
	defer l.disarmMu.Unlock()
	l.disarmed = append(l.disarmed, tm.Seq)
}

func (l *linkTimers) disarms() []uint64 {
	l.disarmMu.Lock()
	defer l.disarmMu.Unlock()
	return append([]uint64(nil), l.disarmed...)
}

type countingFinder struct {
	effects.MessageFinder
	finds atomic.Int32
}

func (c *countingFinder) Find(ctx context.Context, r uint64, keys []store.MsgKey) ([]domain.Message, error) {
	c.finds.Add(1)
	return c.MessageFinder.Find(ctx, r, keys)
}

type failingReplyCount struct{ err error }

func (f failingReplyCount) AddReplyCount(context.Context, store.MsgKey, int) (domain.ReplyCount, error) {
	return domain.ReplyCount{}, f.err
}

type indexRig struct {
	msgs     *memstore.Messages
	rooms    *memstore.Rooms
	inter    *memstore.Interactions
	mentions *memstore.Mentions
	finder   *countingFinder
	js       *publishtest.JetStream
	timers   *linkTimers
	index    *effects.ReplyMentionIndex
}

func newIndexRig(t *testing.T) *indexRig {
	t.Helper()
	msgs := memstore.NewMessages()
	rg := &indexRig{
		msgs: msgs, rooms: memstore.NewRooms(), inter: memstore.NewInteractions(), mentions: memstore.NewMentions(),
		finder: &countingFinder{MessageFinder: msgs}, js: &publishtest.JetStream{}, timers: &linkTimers{},
	}
	createRoom(t, rg.rooms, room)
	rg.index = rg.build(t, rg.deps())
	return rg
}

func (rg *indexRig) deps() effects.ReplyMentionIndexDeps {
	return effects.ReplyMentionIndexDeps{
		Messages: rg.finder, Replies: rg.inter, Counts: rg.msgs, Mentions: rg.mentions, Timers: rg.timers,
		Rooms: rg.rooms, JS: rg.js, Now: func() time.Time { return indexedAt },
	}
}

func (rg *indexRig) build(t *testing.T, d effects.ReplyMentionIndexDeps) *effects.ReplyMentionIndex {
	t.Helper()
	return built(effects.NewReplyMentionIndex(d, effects.ReplyMentionIndexConfig{SubjectRoot: "evt", RoomCache: 16}))(t)
}

func (rg *indexRig) send(t *testing.T, seq uint64, edit func(*domain.Message)) work.Record {
	t.Helper()
	m := domain.Message{Room: room, Seq: seq, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: indexedAt}
	if edit != nil {
		edit(&m)
	}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert %d: %+v", seq, res)
	}
	return work.Record{Kind: store.MessageInserted, Room: room, Seq: seq, Version: uint32(store.ReplyMentionFlagsOf(m)), CommittedAt: indexedAt}
}

func (rg *indexRig) edit(t *testing.T, seq uint64, ver uint32, kind domain.EditKind) work.Record {
	t.Helper()
	e := domain.Edit{Room: room, Seq: seq, Version: ver, Kind: kind, Tenant: tenant, By: "alice", Text: "edited", At: indexedAt.Add(time.Minute)}
	if err := rg.msgs.ApplyEdit(t.Context(), e); err != nil {
		t.Fatalf("ApplyEdit v%d: %v", ver, err)
	}
	return work.Record{Kind: store.EditInserted, Room: room, Seq: seq, Version: ver, CommittedAt: indexedAt}
}

func (rg *indexRig) run(t *testing.T, recs ...work.Record) []error {
	t.Helper()
	return rg.index.Effect().Run(t.Context(), recs)
}

func (rg *indexRig) replies(t *testing.T, parent uint64) domain.ReplyCount {
	t.Helper()
	found, err := rg.msgs.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: parent}})
	if err != nil || len(found) != 1 {
		t.Fatalf("Find %d = %v, %v", parent, found, err)
	}
	return found[0].Replies
}

func (rg *indexRig) liveReplies(t *testing.T, parent uint64) uint32 {
	t.Helper()
	n, err := rg.inter.CountLiveReplies(t.Context(), store.MsgKey{Room: room, Seq: parent})
	if err != nil {
		t.Fatalf("CountLiveReplies: %v", err)
	}
	return n
}

func (rg *indexRig) mentionsOf(t *testing.T, seq uint64) map[domain.MentionTarget]domain.Mention {
	t.Helper()
	got, err := rg.mentions.MentionsOf(t.Context(), store.MsgKey{Room: room, Seq: seq})
	if err != nil {
		t.Fatalf("MentionsOf %d: %v", seq, err)
	}
	out := make(map[domain.MentionTarget]domain.Mention, len(got))
	for _, m := range got {
		out[m.Target] = m
	}
	return out
}

func replyTo(parent uint64) func(*domain.Message) {
	return func(m *domain.Message) { m.ReplyTo = &domain.ReplyRef{Seq: parent} }
}
