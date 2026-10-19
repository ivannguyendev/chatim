package member_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNewRequiresEveryDependency(t *testing.T) {
	rg := newRig(t, nil)
	full := rg.deps(t, nil)
	full.Now, full.NewRequestID = nil, nil
	for name, drop := range map[string]func(d *member.Deps){
		"no access":   func(d *member.Deps) { d.Access = nil },
		"no messages": func(d *member.Deps) { d.Messages = nil },
		"no rooms":    func(d *member.Deps) { d.Rooms = nil },
		"no events":   func(d *member.Deps) { d.Events = nil },
		"no members":  func(d *member.Deps) { d.Members = nil },
		"no requests": func(d *member.Deps) { d.Requests = nil },
		"no forget":   func(d *member.Deps) { d.Forget = nil },
		"no timers":   func(d *member.Deps) { d.Timers = nil },
		"no reads":    func(d *member.Deps) { d.Reads = nil },
	} {
		d := full
		drop(&d)
		if _, err := member.New(d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := member.New(full); err != nil {
		t.Fatalf("New with a default clock and request ids: %v", err)
	}
}

func TestNewRefusesAMemberBatchOutOfBounds(t *testing.T) {
	rg := newRig(t, nil)
	for _, n := range []int{1, 1001, -1} {
		d := rg.deps(t, nil)
		d.Limits = member.Limits{MemberBatch: n}
		_, err := member.New(d)
		if want := (member.Limits{MemberBatch: n}).Validate(); err == nil || want == nil || err.Error() != want.Error() || !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("New with member batch %d = %v, want the Limits error %v", n, err, want)
		}
	}
}
