package sweep

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errNoMemberChange = fmt.Errorf("%w: member doc holds no membership change to publish", apperr.ErrInvalidArgument)
	errNeverCleared   = fmt.Errorf("%w: member history was never cleared", apperr.ErrInvalidArgument)
)

type MemberLookup interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
}

type memberRebuilder struct {
	members MemberLookup
	types   *eventkit.RoomTypes
	now     func() time.Time
}

func NewMemberRebuilder(members MemberLookup, types *eventkit.RoomTypes, now func() time.Time) Rebuilder {
	if now == nil {
		now = time.Now
	}
	return &memberRebuilder{members: members, types: types, now: now}
}

func (r *memberRebuilder) Rebuild(ctx context.Context, doc store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
	found, err := r.members.MembersOf(ctx, doc.Room, []string{doc.User})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMemberNotFound
	}
	m := found[0]
	target := memberTarget(doc, m, bit)
	room, err := r.types.Identity(ctx, doc.Room)
	if err != nil {
		return nil, dropWith(target, err)
	}
	switch bit {
	case store.BitMember:
		ev := pbconv.MemberEvent(room.Type, m)
		if ev == nil {
			return nil, dropWith(target, errNoMemberChange)
		}
		return one(ev), nil
	case store.BitReadPosition:
		entry := &chatimv1.ReadEntry{User: doc.User, ReadSeq: m.ReadSeq, ReadVer: m.ReadVer}
		return one(pbconv.ReadPositions(room, []*chatimv1.ReadEntry{entry}, r.stamp())), nil
	case store.BitHistoryCleared:
		if m.ClearedAt.IsZero() {
			return nil, dropWith(target, errNeverCleared)
		}
		return one(pbconv.HistoryCleared(room, doc.User, m.ClearedAt, r.stamp())), nil
	case store.BitPreferences:
		return one(pbconv.RoomPreferencesChanged(room, m, r.stamp())), nil
	default:
		return nil, unknownBit(doc.Collection, bit)
	}
}

func (r *memberRebuilder) stamp() time.Time { return r.now().UTC().Truncate(time.Millisecond) }

func memberTarget(doc store.PendingDoc, m domain.Member, bit store.StateBit) store.StateTarget {
	t := store.StateTarget{Collection: store.StateMembers, Bit: bit, Room: doc.Room, User: doc.User}
	switch bit {
	case store.BitMember:
		t.Ver = uint64(m.Ver)
	case store.BitReadPosition:
		t.Ver = m.ReadVer
	case store.BitHistoryCleared:
		t.At = m.ClearedAt
	case store.BitPreferences:
		t.Ver = m.Preferences.Ver
	}
	return t
}
