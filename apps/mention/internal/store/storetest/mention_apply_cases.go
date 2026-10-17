package storetest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

func newMessageMentions(t *testing.T, s store.Mentions) {
	k := keyOf(roomA, 1)
	mustApply(t, s, planOf(k, 0, []domain.Target{stamped(targetMinh, secs(1)), stamped(targetOps, secs(2)), stamped(targetAll, secs(3))}, nil))
	assertDocs(t, s, k,
		docOf(k, targetMinh, domain.StateLive, 0, secs(1)),
		docOf(k, targetOps, domain.StateLive, 0, secs(2)),
		docOf(k, targetAll, domain.StateLive, 0, secs(3)))
}

func editRetiresAndReAdds(t *testing.T, s store.Mentions) {
	k := keyOf(roomA, 1)
	mustApply(t, s, planOf(k, 0, []domain.Target{stamped(targetMinh, secs(1)), stamped(targetLan, secs(1))}, nil))
	mustApply(t, s, planOf(k, 1, []domain.Target{stamped(targetLan, secs(1))}, []domain.Target{stamped(targetMinh, secs(1))}))
	assertDocs(t, s, k, docOf(k, targetMinh, domain.StateRetired, 1, secs(1)), docOf(k, targetLan, domain.StateLive, 1, secs(1)))
	mustApply(t, s, planOf(k, 2, []domain.Target{stamped(targetMinh, secs(20)), stamped(targetLan, secs(1))}, nil))
	assertDocs(t, s, k, docOf(k, targetMinh, domain.StateLive, 2, secs(20)), docOf(k, targetLan, domain.StateLive, 2, secs(1)))
	mustApply(t, s, planOf(k, 3, nil, []domain.Target{stamped(targetMinh, secs(20)), stamped(targetLan, secs(1))}))
	assertDocs(t, s, k, docOf(k, targetMinh, domain.StateRetired, 3, secs(20)), docOf(k, targetLan, domain.StateRetired, 3, secs(1)))
}

func olderAfterNewer(t *testing.T, s store.Mentions) {
	k := keyOf(roomA, 1)
	mustApply(t, s, planOf(k, 2, []domain.Target{stamped(targetMinh, secs(20))}, nil))
	mustApply(t, s, planOf(k, 1, nil, []domain.Target{stamped(targetMinh, secs(1))}))
	mustApply(t, s, planOf(k, 0, []domain.Target{stamped(targetMinh, secs(1))}, nil))
	assertDocs(t, s, k, docOf(k, targetMinh, domain.StateLive, 2, secs(20)))
}

func tombstoneFirst(t *testing.T, s store.Mentions) {
	k := keyOf(roomA, 1)
	mustApply(t, s, planOf(k, 1, nil, []domain.Target{stamped(targetMinh, secs(1)), stamped(targetAll, secs(1))}))
	assertDocs(t, s, k, docOf(k, targetMinh, domain.StateRetired, 1, secs(1)), docOf(k, targetAll, domain.StateRetired, 1, secs(1)))
	mustApply(t, s, planOf(k, 0, []domain.Target{stamped(targetMinh, secs(1)), stamped(targetAll, secs(1))}, nil))
	assertDocs(t, s, k, docOf(k, targetMinh, domain.StateRetired, 1, secs(1)), docOf(k, targetAll, domain.StateRetired, 1, secs(1)))
	got, err := s.ListByTarget(t.Context(), store.TargetMentionQuery{Tenant: tenant, Target: targetMinh.Name(roomA), Limit: 10})
	if err != nil || len(got) != 0 {
		t.Fatalf("ListByTarget after a blocked live write = %+v, %v; want nothing", got, err)
	}
}

func samePlanTwice(t *testing.T, s store.Mentions) {
	k := keyOf(roomA, 1)
	edit := planOf(k, 1, []domain.Target{stamped(targetOps, secs(5))}, []domain.Target{stamped(targetMinh, secs(1))})
	mustApply(t, s, edit)
	first := mentionsOf(t, s, k)
	mustApply(t, s, edit)
	again := mentionsOf(t, s, k)
	same := func(a, b domain.Mention) bool {
		return a.State == b.State && a.MessageVer == b.MessageVer && a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
	}
	if len(first) != 2 || !slices.EqualFunc(first, again, same) {
		t.Fatalf("Of after a replay = %+v,\nwant unchanged %+v", again, first)
	}
}

func mentionMessagesApart(t *testing.T, s store.Mentions) {
	one, two := keyOf(roomA, 1), keyOf(roomA, 2)
	mustApply(t, s, planOf(one, 0, []domain.Target{stamped(targetMinh, secs(1))}, nil))
	mustApply(t, s, planOf(two, 0, []domain.Target{stamped(targetMinh, secs(2)), stamped(targetLan, secs(2))}, nil))
	mustApply(t, s, planOf(two, 1, nil, []domain.Target{stamped(targetMinh, secs(2)), stamped(targetLan, secs(2))}))
	assertDocs(t, s, one, docOf(one, targetMinh, domain.StateLive, 0, secs(1)))
	assertDocs(t, s, keyOf(roomA, 3))
}

func mentionTenantsApart(t *testing.T, s store.Mentions) {
	k := keyOf(roomA, 1)
	mustApply(t, s, planOf(k, 0, []domain.Target{stamped(targetMinh, secs(1))}, nil))
	foreign := planOf(k, 5, []domain.Target{stamped(targetLan, secs(9))}, []domain.Target{stamped(targetMinh, secs(9))})
	foreign.Tenant = otherTenant
	mustApply(t, s, foreign)
	assertDocs(t, s, k, docOf(k, targetMinh, domain.StateLive, 0, secs(1)))
	got, err := s.Of(t.Context(), otherTenant, k)
	if err != nil || len(got) != 1 || got[0].Tenant != otherTenant || got[0].Target != targetLan.Identity() {
		t.Fatalf("Of(%s) = %+v, %v; want only its own lan doc", otherTenant, got, err)
	}
}

func invalidMentionCalls(t *testing.T, s store.Mentions) {
	k := keyOf(roomA, 1)
	assertInvalid(t, "Apply(target twice)", s.Apply(t.Context(), planOf(k, 0, []domain.Target{stamped(targetMinh, secs(1))}, []domain.Target{targetMinh})))
	assertInvalid(t, "Apply(live with no time)", s.Apply(t.Context(), planOf(k, 0, []domain.Target{targetMinh}, nil)))
	_, err := s.Of(t.Context(), tenant, keyOf(roomA, 0))
	assertInvalid(t, "Of(seq 0)", err)
	_, err = s.Of(t.Context(), "", k)
	assertInvalid(t, "Of(no tenant)", err)
	assertDocs(t, s, k)
}
