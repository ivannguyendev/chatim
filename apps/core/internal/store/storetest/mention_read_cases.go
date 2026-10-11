package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func mentionReadCases() []mentionCase {
	return []mentionCase{
		{"MarkReadFlipsUserMentionsBothWays", markReadBothWays},
		{"ReapplyKeepsReadAndReaddStartsUnread", reapplyKeepsRead},
		{"MarkReadLeavesRetiredThreadAndOtherRoomsAlone", markReadScope},
		{"ListStillGivesReadMentionsWithTheFlag", listReadMentions},
		{"MarkReadRejectsBadInput", markReadInvalid},
	}
}

func readMention(seq uint64, target domain.MentionTarget, ver uint32, after time.Duration) domain.Mention {
	d := mentionDoc(seq, target, true, ver, after)
	d.Read = true
	return d
}

func mustMarkMentionsRead(t *testing.T, s store.Mentions, room uint64, user string, readSeq uint64) {
	t.Helper()
	if err := s.MarkRead(t.Context(), room, user, readSeq); err != nil {
		t.Fatalf("MarkRead(%d, %q, %d): %v", room, user, readSeq, err)
	}
}

func markReadBothWays(t *testing.T, s store.Mentions) {
	for _, seq := range []uint64{80, 90, 100} {
		mustApplyMentions(t, s, mentionSetOf(seq, 0, 0, mentionMinh, mentionLan, mentionOps))
	}
	for range 2 {
		mustMarkMentionsRead(t, s, roomA, "minh", 95)
		for _, seq := range []uint64{80, 90} {
			assertMentions(t, s, seq, readMention(seq, mentionMinh, 0, 0), mentionDoc(seq, mentionLan, true, 0, 0), mentionDoc(seq, mentionOps, true, 0, 0))
		}
		assertMentions(t, s, 100, mentionDoc(100, mentionMinh, true, 0, 0), mentionDoc(100, mentionLan, true, 0, 0), mentionDoc(100, mentionOps, true, 0, 0))
	}
	for range 2 {
		mustMarkMentionsRead(t, s, roomA, "minh", 79)
		for _, seq := range []uint64{80, 90, 100} {
			assertMentions(t, s, seq, mentionDoc(seq, mentionMinh, true, 0, 0), mentionDoc(seq, mentionLan, true, 0, 0), mentionDoc(seq, mentionOps, true, 0, 0))
		}
	}
}

func reapplyKeepsRead(t *testing.T, s store.Mentions) {
	mustApplyMentions(t, s, mentionSetOf(90, 0, 0, mentionMinh, mentionAll))
	mustMarkMentionsRead(t, s, roomA, "minh", 95)
	mustApplyMentions(t, s, mentionSetOf(90, 1, time.Minute, mentionMinh, mentionAll))
	assertMentions(t, s, 90, readMention(90, mentionMinh, 1, time.Minute), mentionDoc(90, mentionAll, true, 1, time.Minute))
	mustApplyMentions(t, s, mentionSetOf(90, 2, 2*time.Minute, mentionAll))
	assertMentions(t, s, 90, mentionDoc(90, mentionMinh, false, 2, 2*time.Minute), mentionDoc(90, mentionAll, true, 2, 2*time.Minute))
	mustApplyMentions(t, s, mentionSetOf(90, 3, 3*time.Minute, mentionMinh, mentionAll))
	assertMentions(t, s, 90, mentionDoc(90, mentionMinh, true, 3, 3*time.Minute), mentionDoc(90, mentionAll, true, 3, 3*time.Minute))
}

func markReadScope(t *testing.T, s store.Mentions) {
	mustApplyMentions(t, s, mentionSetOf(80, 0, 0, mentionMinh))
	mustApplyMentions(t, s, mentionSetOf(80, 1, time.Minute))
	threaded := mentionSetOf(3, 0, 0, mentionMinh)
	threaded.Key.Thread = 50
	mustApplyMentions(t, s, threaded)
	elsewhere := mentionSetOf(85, 0, 0, mentionMinh)
	elsewhere.Key.Room = roomB
	mustApplyMentions(t, s, elsewhere)
	mustMarkMentionsRead(t, s, roomA, "minh", 95)
	assertMentions(t, s, 80, mentionDoc(80, mentionMinh, false, 1, time.Minute))
	for _, key := range []store.MsgKey{threaded.Key, elsewhere.Key} {
		got, err := s.MentionsOf(t.Context(), key)
		if err != nil || len(got) != 1 || got[0].Read || !got[0].Live {
			t.Fatalf("MentionsOf(%+v) = %+v, %v; want one live unread doc", key, got, err)
		}
	}
}

func listReadMentions(t *testing.T, s store.Mentions) {
	read := placed(t, s, roomA, 90, baseTime, mentionMinh)
	unread := placed(t, s, roomA, 100, baseTime.Add(time.Second), mentionMinh)
	mustMarkMentionsRead(t, s, roomA, "minh", 95)
	got, err := s.List(t.Context(), store.MentionQuery{Tenant: tenant, Targets: wants(roomA, mentionMinh), Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	keys := make([]store.MsgKey, len(got))
	flags := make([]bool, len(got))
	for i, m := range got {
		keys[i], flags[i] = store.MsgKey(m.Key), m.Read
	}
	if !slices.Equal(keys, []store.MsgKey{unread, read}) || !slices.Equal(flags, []bool{false, true}) {
		t.Fatalf("List = %v read %v; want [%v %v] read [false true]", keys, flags, unread, read)
	}
}

func markReadInvalid(t *testing.T, s store.Mentions) {
	assertErrorIs(t, "MarkRead(room 0)", s.MarkRead(t.Context(), 0, "minh", 1), apperr.ErrInvalidArgument)
	assertErrorIs(t, "MarkRead(bad user)", s.MarkRead(t.Context(), roomA, "", 1), apperr.ErrInvalidArgument)
}
