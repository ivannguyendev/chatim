package effects

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MentionReadName = "mention_read"

type MentionReadDeps struct {
	Members  MemberLookup
	Mentions MentionReads
}

type MentionRead struct {
	deps    MentionReadDeps
	dropped atomic.Uint64
}

func NewMentionRead(deps MentionReadDeps) (*MentionRead, error) {
	if deps.Members == nil || deps.Mentions == nil {
		return nil, fmt.Errorf("%w: %s needs members and mentions", apperr.ErrInvalidArgument, MentionReadName)
	}
	return &MentionRead{deps: deps}, nil
}

func (e *MentionRead) Effect() Effect {
	return Effect{Name: MentionReadName, Run: e.run}
}

func (e *MentionRead) Dropped() uint64 { return e.dropped.Load() }

func (e *MentionRead) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	for _, g := range groupRecords(recs, recordRoom) {
		e.runRoom(ctx, g.key, recs, g.indexes, errs)
	}
	return errs
}

func (e *MentionRead) runRoom(ctx context.Context, room uint64, recs []work.Record, indexes []int, errs []error) {
	mine := pick(recs, indexes)
	readers := groupRecords(mine, func(r work.Record) string { return r.User })
	users := make([]string, len(readers))
	for i, g := range readers {
		users[i] = g.key
	}
	found, err := e.deps.Members.MembersOf(ctx, room, users)
	switch {
	case undeliverable(err):
		e.dropped.Add(uint64(len(indexes)))
		return
	case err != nil:
		for _, i := range indexes {
			errs[i] = err
		}
		return
	}
	docs := make(map[string]domain.Member, len(found))
	for _, m := range found {
		docs[m.User] = m
	}
	for _, g := range readers {
		err := e.mark(ctx, room, mine, g, docs)
		for _, j := range g.indexes {
			errs[indexes[j]] = err
		}
	}
}

func (e *MentionRead) mark(ctx context.Context, room uint64, recs []work.Record, g recordGroup[string], docs map[string]domain.Member) error {
	doc, ok := docs[g.key]
	if !ok {
		g.drop(&e.dropped)
		return nil
	}
	for _, i := range g.indexes {
		if doc.ReadVer < uint64(recs[i].Version) {
			return store.ErrStaleRead
		}
	}
	return e.deps.Mentions.MarkRead(ctx, room, g.key, doc.ReadSeq)
}

func pick(recs []work.Record, indexes []int) []work.Record {
	out := make([]work.Record, len(indexes))
	for i, j := range indexes {
		out[i] = recs[j]
	}
	return out
}
