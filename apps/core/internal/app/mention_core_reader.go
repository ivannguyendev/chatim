package app

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/room/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/mention/mentionapp"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type mentionMembers interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
	ListMembers(ctx context.Context, room uint64, after store.MemberCursor, limit int) ([]domain.Member, error)
	RoomsOf(ctx context.Context, tenant, user string) ([]store.MemberRoom, error)
	MemberUserIDs(ctx context.Context, room uint64) ([]string, error)
}

type mentionMessages interface {
	store.MessageTimes
	store.MessageVersions
}

type roomInfoReader interface {
	RoomInfos(ctx context.Context, rooms []uint64) ([]domain.Room, error)
}

type visibleReader interface {
	VisibleMessages(ctx context.Context, tenant, user string, keys []store.MsgKey) (map[store.MsgKey]domain.Message, error)
}

type roomAuthorizer interface {
	Authorize(ctx context.Context, action access.Action, tenant, user string, room uint64) (access.Request, error)
}

type mentionCore struct {
	members  mentionMembers
	messages mentionMessages
	infos    roomInfoReader
	info     *roomcache.InfoCache
	visible  visibleReader
	access   roomAuthorizer
}

var _ mentionapp.CoreReader = mentionCore{}

func (c mentionCore) MembersOf(ctx context.Context, room uint64, users []string) ([]mentionapp.Member, error) {
	ms, err := c.members.MembersOf(ctx, room, users)
	return mentionMembersOf(ms), err
}

func (c mentionCore) ListMembers(ctx context.Context, room uint64, after mentionapp.MemberCursor, limit int) ([]mentionapp.Member, error) {
	cursor := store.MemberCursor{Role: domain.Role(after.Role), Priority: after.Priority, JoinedAt: after.JoinedAt, User: after.User}
	ms, err := c.members.ListMembers(ctx, room, cursor, limit)
	return mentionMembersOf(ms), err
}

func (c mentionCore) RoomMemberIDs(ctx context.Context, room uint64) ([]string, error) {
	return c.members.MemberUserIDs(ctx, room)
}

func (c mentionCore) RoomMemberCount(ctx context.Context, room uint64) (int64, error) {
	mark := c.info.Mark()
	if hit, _ := c.info.Get([]uint64{room}); len(hit) == 1 {
		return int64(hit[room].MemberCount), nil
	}
	loaded, err := c.infos.RoomInfos(ctx, []uint64{room})
	if err != nil {
		return 0, err
	}
	c.info.Put(mark, loaded...)
	for _, r := range loaded {
		if r.ID == room {
			return int64(r.MemberCount), nil
		}
	}
	return 0, apperr.ErrNotFound
}

func (c mentionCore) RoomsOf(ctx context.Context, tenant, user string) ([]mentionapp.MemberRoom, error) {
	rooms, err := c.members.RoomsOf(ctx, tenant, user)
	if err != nil {
		return nil, err
	}
	out := make([]mentionapp.MemberRoom, len(rooms))
	for i, r := range rooms {
		out[i] = mentionapp.MemberRoom{Room: r.Room, JoinedAt: r.JoinedAt, ClearedAt: r.ClearedAt, ReadSeq: r.ReadSeq}
	}
	return out, nil
}

func (c mentionCore) SentTimes(ctx context.Context, keys []mentionapp.MessageKey) (map[mentionapp.MessageKey]time.Time, error) {
	return lookupInChunks(ctx, keys, c.messages.SentTimes)
}

func (c mentionCore) MessageVersions(ctx context.Context, keys []mentionapp.MessageKey) (map[mentionapp.MessageKey]uint32, error) {
	return lookupInChunks(ctx, keys, c.messages.MessageVersions)
}

func (c mentionCore) Authorize(ctx context.Context, tenant, user string, room uint64, action mentionapp.Action) error {
	_, err := c.access.Authorize(ctx, access.Action(action), tenant, user, room)
	return err
}

func mentionMembersOf(ms []domain.Member) []mentionapp.Member {
	if ms == nil {
		return nil
	}
	out := make([]mentionapp.Member, len(ms))
	for i, m := range ms {
		out[i] = mentionapp.Member{User: m.User, Role: string(m.Role), Priority: m.Priority, JoinedAt: m.JoinedAt, Active: m.Active()}
	}
	return out
}
