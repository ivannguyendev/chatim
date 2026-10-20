package member_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMemberBatchDefaultsTo500AndChecksBounds(t *testing.T) {
	rg := newRig(t, nil)
	if got := rg.m.MemberBatch(); got != member.DefaultMemberBatch || member.DefaultMemberBatch != 500 {
		t.Fatalf("MemberBatch() = %d with default %d, want 500", got, member.DefaultMemberBatch)
	}
	for _, n := range []int{member.MinMemberBatch, 2, 1000} {
		d := rg.deps(t, nil)
		d.Limits = member.Limits{MemberBatch: n}
		if got := rg.build(t, d).MemberBatch(); got != n {
			t.Fatalf("MemberBatch() = %d, want %d", got, n)
		}
	}
	for _, n := range []int{1, 1001, -1} {
		err := member.Limits{MemberBatch: n}.Validate()
		if !errors.Is(err, apperr.ErrInvalidArgument) || !strings.Contains(err.Error(), strconv.Itoa(n)) {
			t.Fatalf("Limits{MemberBatch: %d}.Validate() = %v, want ErrInvalidArgument naming it", n, err)
		}
	}
}

func TestLimitsKeepTheMemberBatchErrorText(t *testing.T) {
	for n, want := range map[int]string{
		1:    "invalid argument: member batch 1 must be 2 to 1000",
		1001: "invalid argument: member batch 1001 must be 2 to 1000",
	} {
		if err := (member.Limits{MemberBatch: n}).Validate(); err == nil || err.Error() != want {
			t.Fatalf("Limits{MemberBatch: %d}.Validate() = %v, want %q", n, err, want)
		}
	}
	if err := (member.Limits{}).Validate(); err != nil {
		t.Fatalf("zero Limits.Validate() = %v, want the default accepted", err)
	}
}
