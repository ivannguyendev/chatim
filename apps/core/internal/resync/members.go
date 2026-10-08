package resync

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
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
		record:  func(m domain.Member) work.Record { return memberRecord(store.MemberChanged, m) },
		extra:   memberStateRecords,
		counted: &s.rep.MemberRecords,
	})
}

func (s *scanner) hidden(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.HiddenMessage]{
		name:    "hidden messages",
		limit:   store.MaxHiddenScan,
		full:    ErrHiddenPageFull,
		between: s.deps.Hidden.Between,
		record:  hiddenRecord,
		counted: &s.rep.HiddenRecords,
	})
}

func memberRecord(kind store.ChangeKind, m domain.Member) work.Record {
	return work.RecordOf(store.Change{Kind: kind, Member: m, CommittedAt: m.LastChangeAt})
}

func memberStateRecords(m domain.Member) []work.Record {
	var out []work.Record
	if m.ReadVer >= 1 {
		out = append(out, memberRecord(store.ReadChanged, m))
	}
	if !m.ClearedAt.IsZero() {
		out = append(out, memberRecord(store.HistoryCleared, m))
	}
	return out
}

func hiddenRecord(h domain.HiddenMessage) work.Record {
	return work.RecordOf(store.Change{Kind: store.MessageHidden, Hidden: h, CommittedAt: h.At})
}
