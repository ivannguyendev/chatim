package storetest

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	roomA       uint64 = 7_340_000_001
	roomB       uint64 = 7_340_000_002
	roomC       uint64 = 7_340_000_003
	tenant             = "acme"
	otherTenant        = "globex"
)

var baseTime = time.UnixMilli(1_700_000_000_000).UTC()

var (
	targetMinh = domain.Target{Kind: domain.TargetUser, ID: "minh"}
	targetLan  = domain.Target{Kind: domain.TargetUser, ID: "lan"}
	targetOps  = domain.Target{Kind: domain.TargetGroup, ID: "team:ops"}
	targetAll  = domain.Target{Kind: domain.TargetAll}
)

func secs(n int) time.Time { return baseTime.Add(time.Duration(n) * time.Second) }

func stamped(t domain.Target, at time.Time) domain.Target {
	t.At = at
	return t
}

func assertInvalid(t *testing.T, name string, err error) {
	t.Helper()
	if !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("%s = %v, want ErrInvalidArgument", name, err)
	}
}

func sorted(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

func compareTargets(a, b domain.Target) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), strings.Compare(a.ID, b.ID))
}
