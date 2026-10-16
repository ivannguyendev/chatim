package resync

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var (
	ErrMemberPageFull = errors.New("resync: one instant holds more member changes than a member page")
	ErrHiddenPageFull = errors.New("resync: one instant holds more hidden messages than a hidden page")
)

type Members interface {
	MembersBetween(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Member, error)
}

type Hidden interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.HiddenMessage, error)
}

func (s *scanner) members(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.Member]{
		name:    "members",
		limit:   store.MaxMemberScan,
		full:    ErrMemberPageFull,
		between: s.deps.Members.MembersBetween,
		at:      func(m domain.Member) time.Time { return m.LastChangeAt },
		marks:   s.memberMarks,
	})
}

func (s *scanner) hidden(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.HiddenMessage]{
		name:    "hidden messages",
		limit:   store.MaxHiddenScan,
		full:    ErrHiddenPageFull,
		between: s.deps.Hidden.Between,
		at:      func(h domain.HiddenMessage) time.Time { return h.At },
		marks: func(h domain.HiddenMessage) []mark {
			t := store.StateTarget{Collection: store.StateHidden, Bit: store.BitHidden, Room: h.Room, Thread: h.Thread, Seq: h.Seq, User: h.User}
			return []mark{{t, &s.rep.Hidden}}
		},
	})
}

func (s *scanner) memberMarks(m domain.Member) []mark {
	target := func(bit store.StateBit, ver uint64) store.StateTarget {
		return store.StateTarget{Collection: store.StateMembers, Bit: bit, Room: m.Room, User: m.User, Ver: ver}
	}
	out := []mark{{target(store.BitMember, uint64(m.Ver)), &s.rep.Members}}
	if m.ReadVer >= 1 {
		out = append(out, mark{target(store.BitReadPosition, m.ReadVer), &s.rep.ReadPositions})
	}
	if !m.ClearedAt.IsZero() {
		cleared := target(store.BitHistoryCleared, 0)
		cleared.At = m.ClearedAt
		out = append(out, mark{cleared, &s.rep.HistoryCleared})
	}
	if m.Preferences.Ver >= 1 {
		out = append(out, mark{target(store.BitPreferences, m.Preferences.Ver), &s.rep.Preferences})
	}
	return out
}
