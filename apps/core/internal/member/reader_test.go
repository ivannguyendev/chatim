package member_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNewReaderRequiresEveryDependency(t *testing.T) {
	rg := newRig(t, nil)
	checker, err := access.NewChecker(rg.rooms, nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	full := member.ReaderDeps{Access: checker, Members: rg.rooms}
	want := "invalid argument: member reader needs access and members"
	for name, drop := range map[string]func(d *member.ReaderDeps){
		"no access":  func(d *member.ReaderDeps) { d.Access = nil },
		"no members": func(d *member.ReaderDeps) { d.Members = nil },
	} {
		d := full
		drop(&d)
		_, err := member.NewReader(d)
		if !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewReader = %v, want ErrInvalidArgument", name, err)
		}
		if err.Error() != want {
			t.Fatalf("%s: NewReader = %q, want %q", name, err, want)
		}
	}
	if _, err := member.NewReader(full); err != nil {
		t.Fatalf("NewReader with every dep: %v", err)
	}
}
