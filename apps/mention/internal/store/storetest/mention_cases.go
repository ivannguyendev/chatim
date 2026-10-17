package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

type mentionCase struct {
	name string
	run  func(t *testing.T, s store.Mentions)
}

func RunMentions(t *testing.T, open func(t *testing.T) store.Mentions) {
	t.Helper()
	cases := []mentionCase{
		{"NewMessageStoresOneLiveDocPerTargetAtItsTime", newMessageMentions},
		{"EditRetiresAndReAddsTargets", editRetiresAndReAdds},
		{"OlderVersionAfterANewerOneChangesNothing", olderAfterNewer},
		{"TombstoneBeforeAnyLiveDocBlocksTheOlderLiveWrite", tombstoneFirst},
		{"SamePlanTwiceChangesNothing", samePlanTwice},
		{"SubMillisecondTimesAreStoredInMillis", subMillisecondTimesAreStoredInMillis},
		{"MessagesStayApart", mentionMessagesApart},
		{"AnotherTenantNeverTouchesTheDocs", mentionTenantsApart},
		{"InvalidCallsAreRejected", invalidMentionCalls},
	}
	for _, c := range slices.Concat(cases, mentionListCases()) {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
	t.Run("EveryOrderOfCreateRemoveReAddDeleteEndsTheSame", func(t *testing.T) { everyOrderEndsTheSame(t, open) })
}

func keyOf(room, seq uint64) domain.MessageKey { return domain.MessageKey{Room: room, Seq: seq} }

func planOf(key domain.MessageKey, ver uint32, live, retired []domain.Target) domain.Plan {
	return domain.Plan{Key: key, Tenant: tenant, Sender: "alice", Ver: ver, Live: live, Retired: retired}
}

func mustApply(t *testing.T, s store.Mentions, p domain.Plan) {
	t.Helper()
	if err := s.Apply(t.Context(), p); err != nil {
		t.Fatalf("Apply(v%d live %v retired %v) = %v", p.Ver, p.Live, p.Retired, err)
	}
}

func docOf(key domain.MessageKey, target domain.Target, state domain.State, ver uint32, created time.Time) domain.Mention {
	return domain.Mention{Key: key, Tenant: tenant, Target: target.Identity(), Sender: "alice", State: state, MessageVer: ver, CreatedAt: created}
}

func mentionsOf(t *testing.T, s store.Mentions, key domain.MessageKey) []domain.Mention {
	t.Helper()
	got, err := s.Of(t.Context(), tenant, key)
	if err != nil {
		t.Fatalf("Of(%v) = %v", key, err)
	}
	slices.SortFunc(got, func(a, b domain.Mention) int { return compareTargets(a.Target, b.Target) })
	return got
}

func assertDocs(t *testing.T, s store.Mentions, key domain.MessageKey, want ...domain.Mention) {
	t.Helper()
	got := mentionsOf(t, s, key)
	slices.SortFunc(want, func(a, b domain.Mention) int { return compareTargets(a.Target, b.Target) })
	same := func(a, b domain.Mention) bool {
		ac, bc := a.CreatedAt, b.CreatedAt
		a.CreatedAt, b.CreatedAt, a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
		return a == b && ac.Equal(bc)
	}
	if !slices.EqualFunc(got, want, same) {
		t.Fatalf("Of(%v) = %+v,\nwant %+v", key, got, want)
	}
	for _, m := range got {
		if m.UpdatedAt.IsZero() {
			t.Fatalf("Of(%v) returned %+v with no updated time", key, m)
		}
	}
}
