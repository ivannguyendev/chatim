package storetest

import (
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func replyCountCases() []countCase {
	return []countCase{
		{"reply deltas move the count and its version and stop at zero", replyDeltas},
		{"set reply count writes only over the expected version", replyCountCAS},
		{"reply counts of a missing message write nothing", replyCountMissing},
		{"reply counts need a valid key, delta and version", replyCountInvalid},
	}
}

func withReplies(m domain.Message, n uint32, v uint64) domain.Message {
	m.Replies = domain.ReplyCount{N: n, Version: v}
	return m
}

func mustAddReplyCount(t *testing.T, s CountableMessages, key store.MsgKey, d int, want domain.ReplyCount) {
	t.Helper()
	if got, err := s.AddReplyCount(t.Context(), key, d); err != nil || got != want {
		t.Fatalf("AddReplyCount(%d) = %+v, %v; want %+v", d, got, err, want)
	}
}

func mustSetReplies(t *testing.T, s CountableMessages, key store.MsgKey, base uint64, n uint32, want bool) {
	t.Helper()
	if ok, err := s.SetReplyCount(t.Context(), key, base, n); err != nil || ok != want {
		t.Fatalf("SetReplyCount(base %d, %d) = %v, %v; want %v", base, n, ok, err, want)
	}
}

func replyDeltas(t *testing.T, s CountableMessages) {
	m, other := msg(roomA, mainThread, 1), msg(roomA, mainThread, 2)
	mustInsert(t, s, []domain.Message{m, other})
	key := store.KeyOf(m)
	mustAddReplyCount(t, s, key, 1, domain.ReplyCount{N: 1, Version: 1})
	mustAddReplyCount(t, s, key, 1, domain.ReplyCount{N: 2, Version: 2})
	mustAddReplyCount(t, s, key, -1, domain.ReplyCount{N: 1, Version: 3})
	assertStored(t, s, withReplies(m, 1, 3), other)
	mustAddReplyCount(t, s, key, -5, domain.ReplyCount{N: 0, Version: 4})
	assertStored(t, s, withReplies(m, 0, 4), other)
}

func replyCountCAS(t *testing.T, s CountableMessages) {
	m := msg(roomA, sideThread, 4)
	mustInsert(t, s, []domain.Message{m})
	key := store.KeyOf(m)
	mustSetReplies(t, s, key, 1, 3, false)
	mustSetReplies(t, s, key, 0, 3, true)
	mustSetReplies(t, s, key, 0, 9, false)
	assertStored(t, s, withReplies(m, 3, 1))
	mustAddReplyCount(t, s, key, 1, domain.ReplyCount{N: 4, Version: 2})
	mustSetReplies(t, s, key, 1, 7, false)
	mustSetReplies(t, s, key, 2, 0, true)
	assertStored(t, s, withReplies(m, 0, 3))
}

func replyCountMissing(t *testing.T, s CountableMessages) {
	key := msgKey(roomA, mainThread, 1)
	_, err := s.AddReplyCount(t.Context(), key, 1)
	assertErrorIs(t, "AddReplyCount(missing)", err, domain.ErrMessageNotFound)
	mustSetReplies(t, s, key, 0, 1, false)
	if got, err := s.Find(t.Context(), roomA, []store.MsgKey{key}); err != nil || len(got) != 0 {
		t.Fatalf("Find after reply counts on a missing message = %+v, %v; want nothing", got, err)
	}
}

func replyCountInvalid(t *testing.T, s CountableMessages) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s, []domain.Message{m})
	key, zero := store.KeyOf(m), msgKey(roomA, mainThread, 0)
	_, err := s.AddReplyCount(t.Context(), key, 0)
	assertErrorIs(t, "AddReplyCount(zero delta)", err, apperr.ErrInvalidArgument)
	_, err = s.AddReplyCount(t.Context(), zero, 1)
	assertErrorIs(t, "AddReplyCount(zero seq)", err, apperr.ErrInvalidArgument)
	_, err = s.SetReplyCount(t.Context(), zero, 0, 1)
	assertErrorIs(t, "SetReplyCount(zero seq)", err, apperr.ErrInvalidArgument)
	_, err = s.SetReplyCount(t.Context(), key, math.MaxInt64, 1)
	assertErrorIs(t, "SetReplyCount(base at max int64)", err, apperr.ErrInvalidArgument)
	assertStored(t, s, m)
}
