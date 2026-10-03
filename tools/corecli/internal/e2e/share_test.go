package e2e_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func routes(owner func(uint16) (string, bool)) func(uint16) (slotmap.Route, bool) {
	return func(s uint16) (slotmap.Route, bool) {
		id, owned := owner(s)
		return slotmap.Route{Core: id, Addr: id + ":9000", Owner: owned}, id != ""
	}
}

func TestShareCountsLiveOwnersAndJudgesBalance(t *testing.T) {
	cores := []string{"core-1", "core-2"}
	even := e2e.ShareOf(routes(func(s uint16) (string, bool) { return cores[s%2], true }))
	if even.Unowned != 0 || even.Owned["core-1"] != 512 || even.Owned["core-2"] != 512 || !even.Balanced(2) {
		t.Fatalf("alternating share = %v, want balanced over two cores", even)
	}
	if even.Balanced(3) || even.Balanced(0) {
		t.Fatalf("share %v judged balanced for the wrong core count", even)
	}
	lopsided := e2e.ShareOf(routes(func(s uint16) (string, bool) {
		if s < 900 {
			return "core-1", true
		}
		return "core-2", true
	}))
	if lopsided.Balanced(2) {
		t.Fatalf("share %v judged balanced", lopsided)
	}
	fallback := e2e.ShareOf(routes(func(s uint16) (string, bool) { return "core-2", s%2 == 0 }))
	if fallback.Unowned != slotmap.Count/2 || fallback.Balanced(1) {
		t.Fatalf("share %v must count fallback routes as unowned", fallback)
	}
	if got := fallback.String(); got != "core-2=512 unowned=512" {
		t.Fatalf("String = %q", got)
	}
}
