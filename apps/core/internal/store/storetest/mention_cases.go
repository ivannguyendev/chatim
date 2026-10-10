package storetest

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	mentionMinh = domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"}
	mentionLan  = domain.MentionTarget{Kind: domain.MentionUser, ID: "lan"}
	mentionOps  = domain.MentionTarget{Kind: domain.MentionGroup, ID: "team:ops"}
	mentionAll  = domain.MentionTarget{Kind: domain.MentionAll}
)

type mentionCase struct {
	name string
	run  func(t *testing.T, s store.Mentions)
}

func RunMentions(t *testing.T, open func(t *testing.T) store.Mentions) {
	t.Helper()
	cases := []mentionCase{
		{"NewMessageStoresOneDocPerTarget", newMessageMentions},
		{"SameVersionAgainWritesNothing", sameVersionMentions},
		{"EditSwapsTargets", editSwapsMentions},
		{"DeleteRetiresEveryTarget", deleteRetiresMentions},
		{"OlderVersionIsIgnored", olderMentionsIgnored},
		{"MessagesStayApart", mentionsStayApart},
		{"InvalidSetIsRejected", invalidMentionSet},
	}
	for _, c := range append(cases, mentionListCases()...) {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func mentionSetOf(seq uint64, ver uint32, after time.Duration, targets ...domain.MentionTarget) store.MentionSet {
	return store.MentionSet{
		Key: store.MsgKey{Room: roomA, Seq: seq}, Tenant: tenant, Sender: "alice", Ver: ver,
		Targets: targets, CreatedAt: baseTime, At: baseTime.Add(after),
	}
}

func mentionDoc(seq uint64, target domain.MentionTarget, live bool, ver uint32, after time.Duration) domain.Mention {
	return domain.Mention{
		Key: domain.MsgKey{Room: roomA, Seq: seq}, Tenant: tenant, Target: target, Sender: "alice",
		Live: live, Ver: ver, CreatedAt: baseTime, UpdatedAt: baseTime.Add(after),
	}
}

func mustApplyMentions(t *testing.T, s store.Mentions, set store.MentionSet) {
	t.Helper()
	if err := s.ApplyMentions(t.Context(), set); err != nil {
		t.Fatalf("ApplyMentions(v%d %v) = %v", set.Ver, set.Targets, err)
	}
}

func assertMentions(t *testing.T, s store.Mentions, seq uint64, want ...domain.Mention) {
	t.Helper()
	got, err := s.MentionsOf(t.Context(), store.MsgKey{Room: roomA, Seq: seq})
	if err != nil {
		t.Fatalf("MentionsOf(%d) = %v", seq, err)
	}
	byTarget := func(a, b domain.Mention) int { return compareTargets(a.Target, b.Target) }
	slices.SortFunc(got, byTarget)
	slices.SortFunc(want, byTarget)
	if !slices.EqualFunc(got, want, sameMention) {
		t.Fatalf("MentionsOf(%d) = %+v,\nwant %+v", seq, got, want)
	}
}

func compareTargets(a, b domain.MentionTarget) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), strings.Compare(a.ID, b.ID))
}

func sameMention(a, b domain.Mention) bool {
	ac, bc, au, bu := a.CreatedAt, b.CreatedAt, a.UpdatedAt, b.UpdatedAt
	a.CreatedAt, b.CreatedAt, a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	return a == b && ac.Equal(bc) && au.Equal(bu)
}

func newMessageMentions(t *testing.T, s store.Mentions) {
	mustApplyMentions(t, s, mentionSetOf(1, 0, 0, mentionMinh, mentionOps, mentionAll))
	assertMentions(t, s, 1, mentionDoc(1, mentionMinh, true, 0, 0), mentionDoc(1, mentionOps, true, 0, 0), mentionDoc(1, mentionAll, true, 0, 0))
}

func sameVersionMentions(t *testing.T, s store.Mentions) {
	mustApplyMentions(t, s, mentionSetOf(1, 0, 0, mentionMinh))
	mustApplyMentions(t, s, mentionSetOf(1, 0, time.Minute, mentionMinh))
	assertMentions(t, s, 1, mentionDoc(1, mentionMinh, true, 0, 0))
}

func editSwapsMentions(t *testing.T, s store.Mentions) {
	mustApplyMentions(t, s, mentionSetOf(1, 0, 0, mentionMinh, mentionLan))
	mustApplyMentions(t, s, mentionSetOf(1, 2, time.Minute, mentionMinh, mentionOps))
	assertMentions(t, s, 1, mentionDoc(1, mentionMinh, true, 2, time.Minute), mentionDoc(1, mentionLan, false, 2, time.Minute), mentionDoc(1, mentionOps, true, 2, time.Minute))
	mustApplyMentions(t, s, mentionSetOf(1, 3, 2*time.Minute, mentionLan))
	assertMentions(t, s, 1, mentionDoc(1, mentionMinh, false, 3, 2*time.Minute), mentionDoc(1, mentionLan, true, 3, 2*time.Minute), mentionDoc(1, mentionOps, false, 3, 2*time.Minute))
}

func deleteRetiresMentions(t *testing.T, s store.Mentions) {
	mustApplyMentions(t, s, mentionSetOf(1, 0, 0, mentionMinh, mentionAll))
	mustApplyMentions(t, s, mentionSetOf(1, 1, time.Minute))
	assertMentions(t, s, 1, mentionDoc(1, mentionMinh, false, 1, time.Minute), mentionDoc(1, mentionAll, false, 1, time.Minute))
	mustApplyMentions(t, s, mentionSetOf(1, 1, 2*time.Minute))
	assertMentions(t, s, 1, mentionDoc(1, mentionMinh, false, 1, time.Minute), mentionDoc(1, mentionAll, false, 1, time.Minute))
}

func olderMentionsIgnored(t *testing.T, s store.Mentions) {
	mustApplyMentions(t, s, mentionSetOf(1, 2, 0, mentionMinh))
	mustApplyMentions(t, s, mentionSetOf(1, 1, time.Minute, mentionLan))
	mustApplyMentions(t, s, mentionSetOf(1, 0, 2*time.Minute, mentionOps))
	assertMentions(t, s, 1, mentionDoc(1, mentionMinh, true, 2, 0))
}

func mentionsStayApart(t *testing.T, s store.Mentions) {
	mustApplyMentions(t, s, mentionSetOf(1, 0, 0, mentionMinh))
	mustApplyMentions(t, s, mentionSetOf(2, 0, 0, mentionMinh, mentionLan))
	mustApplyMentions(t, s, mentionSetOf(2, 1, time.Minute))
	assertMentions(t, s, 1, mentionDoc(1, mentionMinh, true, 0, 0))
	assertMentions(t, s, 3)
}

func invalidMentionSet(t *testing.T, s store.Mentions) {
	bad := mentionSetOf(1, 0, 0, mentionMinh, mentionMinh)
	if err := s.ApplyMentions(t.Context(), bad); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("ApplyMentions(duplicate target) = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.MentionsOf(t.Context(), store.MsgKey{Room: roomA}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("MentionsOf(seq 0) = %v, want ErrInvalidArgument", err)
	}
	assertMentions(t, s, 1)
}
