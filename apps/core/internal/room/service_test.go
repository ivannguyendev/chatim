package room_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/room"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNewRequiresEveryDependency(t *testing.T) {
	rg := newRig(t, nil)
	full := rg.deps(t, nil)
	full.Now = nil
	for name, drop := range map[string]func(d *room.Deps){
		"no access":   func(d *room.Deps) { d.Access = nil },
		"no rooms":    func(d *room.Deps) { d.Rooms = nil },
		"no directs":  func(d *room.Deps) { d.Directs = nil },
		"no members":  func(d *room.Deps) { d.Members = nil },
		"no requests": func(d *room.Deps) { d.Requests = nil },
		"no events":   func(d *room.Deps) { d.Events = nil },
	} {
		d := full
		drop(&d)
		if _, err := room.New(d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := room.New(full); err != nil {
		t.Fatalf("New with a default clock and room ids: %v", err)
	}
}
