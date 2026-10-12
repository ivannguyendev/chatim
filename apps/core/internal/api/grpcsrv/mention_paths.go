package grpcsrv

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Service) userMentions(ctx context.Context, r mentionRead) (store.MentionSource, error) {
	q := store.TargetMentionQuery{
		Tenant: r.who.tenant, Target: domain.MentionTarget{Kind: domain.MentionUser, ID: r.who.user}.Name(0), UnreadOnly: r.hide, Limit: r.limit,
	}
	fetch := func(before store.MentionCursor) ([]domain.Mention, error) {
		q.Before = before
		return s.mentionList.ListByTarget(ctx, q)
	}
	keep := func(raw []domain.Mention) ([]domain.Mention, error) {
		in := slices.DeleteFunc(slices.Clone(raw), func(d domain.Mention) bool {
			room, ok := r.rooms[d.Key.Room]
			return !ok || !d.CreatedAt.After(store.MentionSince(room))
		})
		if !r.hide {
			return in, nil
		}
		if err := s.readPoints(ctx, r, mentionRooms(in)); err != nil {
			return nil, err
		}
		return slices.DeleteFunc(in, r.isRead), nil
	}
	return store.ScanMentions(r.limit, r.before, fetch, keep)
}

func (s *Service) sharedMentions(ctx context.Context, r mentionRead, rooms []store.MemberRoom) (store.MentionSource, error) {
	if len(rooms) == 0 {
		return store.MentionSource{}, nil
	}
	ids := make([]uint64, len(rooms))
	for i, room := range rooms {
		ids[i] = room.Room
	}
	last, err := s.memberRooms.SharedMentionTimes(ctx, ids)
	if err != nil {
		return store.MentionSource{}, err
	}
	plan := store.SharedMentionRooms(rooms, last)
	var got store.MentionSource
	for i := 0; i < len(plan); {
		batch := plan[i:min(i+store.SharedMentionBatch, len(plan))]
		i += len(batch)
		src, err := s.sharedBatch(ctx, r, batch)
		if err != nil {
			return store.MentionSource{}, err
		}
		got = store.JoinMentionSources(r.limit, got, src)
		if i < len(plan) && store.MentionsSettled(r.limit, got, plan[i].Last) {
			break
		}
	}
	return got, nil
}

func (s *Service) sharedBatch(ctx context.Context, r mentionRead, batch []store.MentionRoom) (store.MentionSource, error) {
	if r.hide {
		ids := make([]uint64, len(batch))
		for i, room := range batch {
			ids[i] = room.Room
		}
		if err := s.readPoints(ctx, r, ids); err != nil {
			return store.MentionSource{}, err
		}
		if batch = store.RaiseSinceToRead(batch, r.readAt); len(batch) == 0 {
			return store.MentionSource{}, nil
		}
	}
	q := store.SharedMentionQuery{
		Tenant: r.who.tenant, Rooms: make([]store.SharedRoom, len(batch)), Groups: r.groupIDs, GroupSince: r.groupSince, Limit: r.limit,
	}
	for i, room := range batch {
		q.Rooms[i] = store.SharedRoom{Room: room.Room, Since: room.Since}
	}
	fetch := func(before store.MentionCursor) ([]domain.Mention, error) {
		q.Before = before
		return s.mentionList.ListShared(ctx, q)
	}
	keep := func(raw []domain.Mention) ([]domain.Mention, error) {
		return slices.DeleteFunc(slices.Clone(raw), func(d domain.Mention) bool { return !r.sharedWanted(d) }), nil
	}
	return store.ScanMentions(r.limit, r.before, fetch, keep)
}

func (r mentionRead) sharedWanted(d domain.Mention) bool {
	if d.Target.Kind == domain.MentionGroup {
		since, mine := r.groups[d.Target.ID]
		if !mine || d.CreatedAt.Before(since) {
			return false
		}
	}
	return !r.hide || !r.isRead(d)
}

func (s *Service) readPoints(ctx context.Context, r mentionRead, rooms []uint64) error {
	var keys []store.MsgKey
	for _, room := range rooms {
		if r.readKnown[room] {
			continue
		}
		r.readKnown[room] = true
		if seq := r.rooms[room].ReadSeq; seq > 0 {
			keys = append(keys, store.MsgKey{Room: room, Seq: seq})
		}
	}
	if len(keys) == 0 {
		return nil
	}
	times, err := s.sentTimes.SentTimes(ctx, keys)
	if err != nil {
		return err
	}
	for k, at := range times {
		r.readAt[k.Room] = at
	}
	return nil
}

func (r mentionRead) isRead(d domain.Mention) bool {
	at, ok := r.readAt[d.Key.Room]
	return ok && !d.CreatedAt.After(at)
}

func mentionRooms(docs []domain.Mention) []uint64 {
	out := make([]uint64, 0, len(docs))
	for _, d := range docs {
		if !slices.Contains(out, d.Key.Room) {
			out = append(out, d.Key.Room)
		}
	}
	return out
}
