package projection

import (
	"context"
	"fmt"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const MentionReadName = "mention_read"

type MentionRead struct {
	members  MemberLookup
	mentions MentionReads
}

type reader struct {
	room uint64
	user string
}

type readWant struct {
	ver     uint64
	indexes []int
}

type readBatch struct {
	rooms []uint64
	users map[uint64][]string
	wants map[reader]readWant
}

func NewMentionRead(members MemberLookup, mentions MentionReads) (*MentionRead, error) {
	if members == nil || mentions == nil {
		return nil, fmt.Errorf("%w: %s needs members and mentions", apperr.ErrInvalidArgument, MentionReadName)
	}
	return &MentionRead{members: members, mentions: mentions}, nil
}

func (r *MentionRead) Name() string { return MentionReadName }

func (r *MentionRead) Keeps(ev Event) bool {
	_, ok := ev.Proto.GetPayload().(*chatimv1.Event_ReadUpdated)
	return ok
}

func (r *MentionRead) Handle(ctx context.Context, evs []Event) []error {
	errs := make([]error, len(evs))
	b := readBatch{users: make(map[uint64][]string), wants: make(map[reader]readWant)}
	for i, ev := range evs {
		u, ok := ev.Proto.GetPayload().(*chatimv1.Event_ReadUpdated)
		if !ok || u.ReadUpdated.GetUser() == "" {
			errs[i] = fmt.Errorf("%w: %s does not keep %T", apperr.ErrInvalidArgument, MentionReadName, ev.Proto.GetPayload())
			continue
		}
		b.add(ev.Room, u.ReadUpdated, i)
	}
	for _, room := range b.rooms {
		for users := range slices.Chunk(b.users[room], domain.MaxMemberBatch) {
			r.markRoom(ctx, room, users, b.wants, errs)
		}
	}
	return errs
}

func (b *readBatch) add(room uint64, u *chatimv1.ReadUpdated, i int) {
	k := reader{room: room, user: u.GetUser()}
	w, seen := b.wants[k]
	if !seen {
		if _, known := b.users[room]; !known {
			b.rooms = append(b.rooms, room)
		}
		b.users[room] = append(b.users[room], k.user)
	}
	w.ver = max(w.ver, u.GetReadVer())
	w.indexes = append(w.indexes, i)
	b.wants[k] = w
}

func (r *MentionRead) markRoom(ctx context.Context, room uint64, users []string, wants map[reader]readWant, errs []error) {
	found, err := r.members.MembersOf(ctx, room, users)
	docs := make(map[string]domain.Member, len(found))
	for _, m := range found {
		docs[m.User] = m
	}
	for _, user := range users {
		w := wants[reader{room: room, user: user}]
		markErr := err
		if markErr == nil {
			markErr = r.mark(ctx, room, user, w.ver, docs)
		}
		for _, i := range w.indexes {
			errs[i] = markErr
		}
	}
}

func (r *MentionRead) mark(ctx context.Context, room uint64, user string, ver uint64, docs map[string]domain.Member) error {
	doc, ok := docs[user]
	switch {
	case !ok:
		return fmt.Errorf("read position of %s in room %d: %w", user, room, domain.ErrMemberNotFound)
	case doc.ReadVer < ver:
		return store.ErrStaleRead
	}
	return r.mentions.MarkRead(ctx, room, user, doc.ReadSeq)
}
