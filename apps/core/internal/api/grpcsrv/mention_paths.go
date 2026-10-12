package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Service) userMentions(ctx context.Context, r mentionRead) ([]domain.Mention, store.MentionCursor, error) {
	target := domain.MentionTarget{Kind: domain.MentionUser, ID: r.who.user}.Name(0)
	docs, err := s.mentionList.ListByTarget(ctx, store.TargetMentionQuery{
		Tenant: r.who.tenant, Target: target, UnreadOnly: r.hide, Before: r.before, Limit: r.limit,
	})
	if err != nil {
		return nil, store.MentionCursor{}, err
	}
	var end store.MentionCursor
	if len(docs) == r.limit {
		end = store.CursorOf(docs[len(docs)-1])
	}
	kept := make([]domain.Mention, 0, len(docs))
	for _, d := range docs {
		room, ok := r.rooms[d.Key.Room]
		if !ok || !d.CreatedAt.After(store.MentionSince(room)) {
			continue
		}
		if d.Read = readUpTo(d, room); !r.hide || !d.Read {
			kept = append(kept, d)
		}
	}
	return kept, end, nil
}

func (s *Service) sharedMentions(ctx context.Context, r mentionRead, rooms []store.MemberRoom) ([]domain.Mention, error) {
	if len(rooms) == 0 {
		return nil, nil
	}
	ids := make([]uint64, len(rooms))
	for i, room := range rooms {
		ids[i] = room.Room
	}
	last, err := s.memberRooms.SharedMentionTimes(ctx, ids)
	if err != nil {
		return nil, err
	}
	plan := store.SharedMentionRooms(rooms, last)
	var got []domain.Mention
	for i := 0; i < len(plan); {
		batch := plan[i:min(i+store.SharedMentionBatch, len(plan))]
		i += len(batch)
		docs, err := s.sharedBatch(ctx, r, batch)
		if err != nil {
			return nil, err
		}
		got = store.MergeMentions(r.limit, got, docs)
		if i < len(plan) && store.MentionsSettled(got, r.limit, plan[i].Last) {
			break
		}
	}
	return got, nil
}

func (s *Service) sharedBatch(ctx context.Context, r mentionRead, batch []store.MentionRoom) ([]domain.Mention, error) {
	if r.hide {
		readAt, err := s.sentTimes.SentTimes(ctx, store.ReadMessageKeys(batch))
		if err != nil {
			return nil, err
		}
		if batch = store.RaiseSinceToRead(batch, readAt); len(batch) == 0 {
			return nil, nil
		}
	}
	q := store.SharedMentionQuery{Rooms: make([]store.SharedRoom, len(batch)), Groups: r.groupIDs, Before: r.before, Limit: r.limit}
	for i, room := range batch {
		q.Rooms[i] = store.SharedRoom{Room: room.Room, Since: room.Since}
	}
	var kept []domain.Mention
	for {
		docs, err := s.mentionList.ListShared(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			if d, ok := r.sharedHit(d); ok {
				kept = append(kept, d)
			}
		}
		kept = store.MergeMentions(r.limit, kept)
		if len(docs) < q.Limit || len(kept) >= r.limit {
			return kept, nil
		}
		q.Before = store.CursorOf(docs[len(docs)-1])
	}
}

func (r mentionRead) sharedHit(d domain.Mention) (domain.Mention, bool) {
	if d.Target.Kind == domain.MentionGroup {
		since, mine := r.groups[d.Target.ID]
		if !mine || d.CreatedAt.Before(since) {
			return d, false
		}
	}
	d.Read = readUpTo(d, r.rooms[d.Key.Room])
	return d, !r.hide || !d.Read
}
