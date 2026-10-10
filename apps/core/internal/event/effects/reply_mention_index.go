package effects

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const ReplyMentionIndexName = "reply_mention_index"

type ReplyMentionIndexDeps struct {
	Messages MessageFinder
	Replies  ReplyLinks
	Counts   ReplyCounter
	Mentions MentionWriter
	Timers   LinkTimers
	Rooms    RoomReader
	JS       publish.JetStream
	Now      func() time.Time
}

type ReplyMentionIndexConfig struct {
	SubjectRoot string
	RoomCache   int
}

type ReplyMentionIndex struct {
	eventPublisher
	deps ReplyMentionIndexDeps
}

func NewReplyMentionIndex(deps ReplyMentionIndexDeps, cfg ReplyMentionIndexConfig) (*ReplyMentionIndex, error) {
	if !deps.complete() || cfg.SubjectRoot == "" || cfg.RoomCache < 0 {
		return nil, fmt.Errorf("%w: %s needs messages, replies, counts, mentions, timers, rooms, a jetstream client and a subject root", apperr.ErrInvalidArgument, ReplyMentionIndexName)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	e := &ReplyMentionIndex{deps: deps}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cmp.Or(cfg.RoomCache, DefaultRoomCache))
	return e, nil
}

func (d ReplyMentionIndexDeps) complete() bool {
	return d.Messages != nil && d.Replies != nil && d.Counts != nil && d.Mentions != nil && d.Timers != nil && d.Rooms != nil && d.JS != nil
}

func (e *ReplyMentionIndex) Effect() Effect {
	return Effect{Name: ReplyMentionIndexName, Run: e.run}
}

func (e *ReplyMentionIndex) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	for _, g := range groupRecords(recs, recordRoom) {
		e.runRoom(ctx, g.key, recs, g.indexes, errs)
	}
	return errs
}

func (e *ReplyMentionIndex) runRoom(ctx context.Context, room uint64, recs []work.Record, indexes []int, errs []error) {
	var wanted []int
	var keys []store.MsgKey
	seen := make(map[store.MsgKey]struct{}, len(indexes))
	for _, i := range indexes {
		if !needsIndex(recs[i]) {
			continue
		}
		wanted = append(wanted, i)
		if k := recordKey(recs[i]); !has(seen, k) {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	found, err := e.deps.Messages.Find(ctx, room, keys)
	if err != nil {
		for _, i := range wanted {
			errs[i] = err
		}
		return
	}
	byKey := make(map[store.MsgKey]domain.Message, len(found))
	for _, m := range found {
		byKey[store.KeyOf(m)] = m
	}
	for _, i := range wanted {
		m, ok := byKey[recordKey(recs[i])]
		if !ok {
			e.dropped.Add(1)
			continue
		}
		errs[i] = e.index(ctx, recs[i], m)
	}
}

func needsIndex(r work.Record) bool {
	return (r.Kind == store.MessageInserted || r.Kind == store.EditInserted) && r.Version != 0
}

func (e *ReplyMentionIndex) index(ctx context.Context, r work.Record, m domain.Message) error {
	flags := store.ReplyMentionFlags(r.Version)
	inserted := r.Kind == store.MessageInserted
	var replyErr, mentionErr error
	if m.ReplyTo != nil && ((inserted && flags&store.HasReply != 0) || (!inserted && m.Deleted)) {
		replyErr = e.settled(e.link(ctx, m))
	}
	if !inserted || flags&store.HasMention != 0 {
		mentionErr = e.settled(e.mention(ctx, m))
	}
	return errors.Join(replyErr, mentionErr)
}

func (e *ReplyMentionIndex) settled(err error) error {
	if gone(err) {
		e.dropped.Add(1)
		return nil
	}
	return err
}

func has[K comparable](m map[K]struct{}, k K) bool {
	_, ok := m[k]
	return ok
}
