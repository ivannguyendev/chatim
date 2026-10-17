package storetest

import (
	"fmt"
	"testing"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

func lifecyclePlans(k domain.MessageKey) []domain.Plan {
	minhSent, minhReAdded, lanSent := stamped(targetMinh, secs(1)), stamped(targetMinh, secs(30)), stamped(targetLan, secs(1))
	return []domain.Plan{
		planOf(k, 0, []domain.Target{minhSent, lanSent}, nil),
		planOf(k, 1, []domain.Target{lanSent}, []domain.Target{minhSent}),
		planOf(k, 2, []domain.Target{lanSent, minhReAdded}, nil),
		planOf(k, 3, nil, []domain.Target{lanSent, minhReAdded}),
	}
}

func everyOrderEndsTheSame(t *testing.T, open func(t *testing.T) store.Mentions) {
	k := keyOf(roomA, 41)
	plans := lifecyclePlans(k)
	edited := []domain.Mention{docOf(k, targetLan, domain.StateLive, 2, secs(1)), docOf(k, targetMinh, domain.StateLive, 2, secs(30))}
	deleted := []domain.Mention{docOf(k, targetLan, domain.StateRetired, 3, secs(1)), docOf(k, targetMinh, domain.StateRetired, 3, secs(30))}
	for _, order := range permutations(3) {
		t.Run(fmt.Sprint("edits", order), func(t *testing.T) { applyInOrder(t, open(t), plans, order, k, edited) })
	}
	for _, order := range permutations(4) {
		t.Run(fmt.Sprint("all", order), func(t *testing.T) { applyInOrder(t, open(t), plans, order, k, deleted) })
	}
}

func applyInOrder(t *testing.T, s store.Mentions, plans []domain.Plan, order []int, k domain.MessageKey, want []domain.Mention) {
	t.Helper()
	for _, i := range order {
		mustApply(t, s, plans[i])
	}
	assertDocs(t, s, k, want...)
}

func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, rest := range permutations(n - 1) {
		for at := 0; at <= len(rest); at++ {
			p := make([]int, 0, n)
			p = append(p, rest[:at]...)
			p = append(p, n-1)
			out = append(out, append(p, rest[at:]...))
		}
	}
	return out
}
