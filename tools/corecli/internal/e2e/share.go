package e2e

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

type Share struct {
	Owned   map[string]int
	Unowned int
}

func ShareOf(slot func(uint16) (slotmap.Route, bool)) Share {
	sh := Share{Owned: map[string]int{}}
	for s := range uint16(slotmap.Count) {
		if rt, ok := slot(s); ok && rt.Owner {
			sh.Owned[rt.Core]++
			continue
		}
		sh.Unowned++
	}
	return sh
}

func (sh Share) Balanced(cores int) bool {
	if cores <= 0 || sh.Unowned > 0 || len(sh.Owned) != cores {
		return false
	}
	least := slotmap.Count/cores - cores
	for _, n := range sh.Owned {
		if n < least {
			return false
		}
	}
	return true
}

func (sh Share) String() string {
	var b strings.Builder
	for _, id := range slices.Sorted(maps.Keys(sh.Owned)) {
		fmt.Fprintf(&b, "%s=%d ", id, sh.Owned[id])
	}
	fmt.Fprintf(&b, "unowned=%d", sh.Unowned)
	return b.String()
}
