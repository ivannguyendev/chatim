package memstore

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type interactionFlagKey struct {
	userKey
	kind keys.InteractionKind
}

type pinFlagKey struct{ room, pv uint64 }

func (s *Interactions) pendingDocs(store.StateCollection) []pendingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return flagged(s.flags, func(k interactionFlagKey) (store.PendingDoc, []byte) {
		d := store.PendingDoc{Collection: store.StateInteractions, Room: k.key.Room, Thread: k.key.Thread, Seq: k.key.Seq, User: k.user, Kind: k.kind}
		return d, keys.InteractionUser(keys.Msg(k.key.Room, k.key.Thread, k.key.Seq), k.kind, k.user)
	})
}

func (s *Interactions) applyStates(targets []store.StateTarget, mark bool, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range targets {
		k := interactionFlagOf(t)
		if ver, ok := s.interactionVerLocked(k); ok && uint64(ver) == t.Ver {
			s.flags.apply(k, t.Bit, mark, at)
		}
	}
}

func (s *Interactions) interactionVerLocked(k interactionFlagKey) (uint32, bool) {
	if k.kind == keys.ReactionKind {
		r, ok := s.reactions[k.userKey]
		return r.N, ok
	}
	b, ok := s.bookmarks[k.userKey]
	return b.Ver, ok
}

func (s *Interactions) seedState(t store.StateTarget, at time.Time) error {
	if t.Ver > math.MaxUint32 {
		return fmt.Errorf("seed interaction of %d/%d/%d: ver %d over uint32", t.Room, t.Thread, t.Seq, t.Ver)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := interactionFlagOf(t)
	ver := uint32(min(t.Ver, math.MaxUint32))
	if k.kind == keys.ReactionKind {
		r := s.reactions[k.userKey]
		r.Room, r.Thread, r.Seq, r.User, r.N = t.Room, t.Thread, t.Seq, t.User, ver
		s.reactions[k.userKey] = r
	} else {
		b := s.bookmarks[k.userKey]
		b.Room, b.Thread, b.Seq, b.User, b.Ver = t.Room, t.Thread, t.Seq, t.User, ver
		s.bookmarks[k.userKey] = b
	}
	s.flags.raise(k, t.Bit, at)
	return nil
}

func interactionFlagOf(t store.StateTarget) interactionFlagKey {
	return interactionFlagKey{userKey: userKey{key: store.MsgKey{Room: t.Room, Thread: t.Thread, Seq: t.Seq}, user: t.User}, kind: t.Kind}
}

func (s *Pins) pendingDocs(store.StateCollection) []pendingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return flagged(s.flags, func(k pinFlagKey) (store.PendingDoc, []byte) {
		return store.PendingDoc{Collection: store.StatePins, Room: k.room, Seq: k.pv}, keys.Pin(k.room, k.pv)
	})
}

func (s *Pins) applyStates(targets []store.StateTarget, mark bool, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range targets {
		if _, ok := slices.BinarySearchFunc(s.facts[t.Room], t.Seq, byPV); ok {
			s.flags.apply(pinFlagKey{t.Room, t.Seq}, t.Bit, mark, at)
		}
	}
}

func (s *Pins) seedState(t store.StateTarget, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	line := s.facts[t.Room]
	if i, ok := slices.BinarySearchFunc(line, t.Seq, byPV); !ok {
		s.facts[t.Room] = slices.Insert(line, i, domain.PinAction{Room: t.Room, PV: t.Seq})
	}
	s.flags.raise(pinFlagKey{t.Room, t.Seq}, t.Bit, at)
	return nil
}

func (s *Hidden) pendingDocs(store.StateCollection) []pendingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return flagged(s.flags, func(k hiddenKey) (store.PendingDoc, []byte) {
		d := store.PendingDoc{Collection: store.StateHidden, Room: k.key.Room, Thread: k.key.Thread, Seq: k.key.Seq, User: k.user}
		return d, append(keys.Msg(k.key.Room, k.key.Thread, k.key.Seq), k.user...)
	})
}

func (s *Hidden) applyStates(targets []store.StateTarget, mark bool, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range targets {
		k := hiddenKey{user: t.User, key: store.MsgKey{Room: t.Room, Thread: t.Thread, Seq: t.Seq}}
		if _, ok := s.seqs[k]; ok {
			s.flags.apply(k, t.Bit, mark, at)
		}
	}
}

func (s *Hidden) seedState(t store.StateTarget, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := hiddenKey{user: t.User, key: store.MsgKey{Room: t.Room, Thread: t.Thread, Seq: t.Seq}}
	if _, ok := s.seqs[k]; !ok {
		s.seqs[k] = millis(at)
	}
	s.flags.raise(k, t.Bit, at)
	return nil
}
