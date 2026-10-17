package memstore

import (
	"math"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type editFlagKey struct {
	key store.MsgKey
	ver uint32
}

func (s *Messages) pendingDocs(store.StateCollection) []pendingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return flagged(s.flags, func(k store.MsgKey) (store.PendingDoc, []byte) {
		return store.PendingDoc{Collection: store.StateMessages, Room: k.Room, Thread: k.Thread, Seq: k.Seq}, keys.Msg(k.Room, k.Thread, k.Seq)
	})
}

func (s *Messages) applyStates(targets []store.StateTarget, mark bool, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range targets {
		k := store.MsgKey{Room: t.Room, Thread: t.Thread, Seq: t.Seq}
		if m, ok := s.messageLocked(k); ok && messageVersionIs(*m, t) {
			s.flags.apply(k, t.Bit, mark, at)
		}
	}
}

func (s *Messages) seedState(t store.StateTarget, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := store.MsgKey{Room: t.Room, Thread: t.Thread, Seq: t.Seq}
	m, ok := s.messageLocked(k)
	if !ok {
		tl := timeline{k.Room, k.Thread}
		line := s.lines[tl]
		i, _ := slices.BinarySearchFunc(line, k.Seq, bySeq)
		s.lines[tl] = slices.Insert(line, i, domain.Message{Room: k.Room, Thread: k.Thread, Seq: k.Seq})
		m, _ = s.messageLocked(k)
	}
	switch t.Bit {
	case store.BitReactionCounts:
		m.Reactions.Version = t.Ver
	case store.BitReplyCounts:
		m.Replies.Version = t.Ver
	default:
	}
	s.flags.raise(k, t.Bit, at)
	return nil
}

func messageVersionIs(m domain.Message, t store.StateTarget) bool {
	switch t.Bit {
	case store.BitReactionCounts:
		return m.Reactions.Version == t.Ver
	case store.BitReplyCounts:
		return m.Replies.Version == t.Ver
	default:
		return true
	}
}

func (s *Edits) pendingDocs(store.StateCollection) []pendingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return flagged(s.flags, func(k editFlagKey) (store.PendingDoc, []byte) {
		d := store.PendingDoc{Collection: store.StateEdits, Room: k.key.Room, Thread: k.key.Thread, Seq: k.key.Seq, Ver: uint64(k.ver)}
		return d, keys.Edit(k.key.Room, k.key.Thread, k.key.Seq, k.ver)
	})
}

func (s *Edits) applyStates(targets []store.StateTarget, mark bool, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range targets {
		k := editFlagOf(t)
		if _, ok := slices.BinarySearchFunc(s.facts[k.key], k.ver, byVersion); ok {
			s.flags.apply(k, t.Bit, mark, at)
		}
	}
}

func (s *Edits) seedState(t store.StateTarget, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := editFlagOf(t)
	line := s.facts[k.key]
	if i, ok := slices.BinarySearchFunc(line, k.ver, byVersion); !ok {
		s.facts[k.key] = slices.Insert(line, i, domain.Edit{Room: t.Room, Thread: t.Thread, Seq: t.Seq, Version: k.ver})
	}
	s.flags.raise(k, t.Bit, at)
	return nil
}

func editFlagOf(t store.StateTarget) editFlagKey {
	return editFlagKey{key: store.MsgKey{Room: t.Room, Thread: t.Thread, Seq: t.Seq}, ver: uint32(min(t.Ver, math.MaxUint32))}
}
