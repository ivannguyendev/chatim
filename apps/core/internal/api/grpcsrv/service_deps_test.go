package grpcsrv_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func depsOf(t *testing.T, rg *rig, o options) grpcsrv.Deps {
	t.Helper()
	board, err := pinproj.New(rg.pins, rg.rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	members := newMemberService(t, rg, o)
	return grpcsrv.Deps{
		Sender: o.sender, Rooms: rg.rooms, Pages: o.pageReader(rg.msgs), Policy: o.policy,
		Mutator: newMutator(t, rg, o), MemberService: members, RoomService: newRoomService(t, rg, o, members), Edits: rg.edits, Hidden: rg.hidden, Bookmarks: rg.reactions, Replies: rg.reactions,
		Mine: rg.reactions, RoomList: newRoomLister(t, rg),
		Threads: rg.threads, Reactors: rg.reactions, PinBoard: board, MemberList: rg.rooms,
	}
}

func TestNewRequiresEveryDependency(t *testing.T) {
	rg := memStores()
	full := depsOf(t, rg, options{sender: &fakeSender{}})
	for name, drop := range map[string]func(d *grpcsrv.Deps){
		"no sender":         func(d *grpcsrv.Deps) { d.Sender = nil },
		"no rooms":          func(d *grpcsrv.Deps) { d.Rooms = nil },
		"no pages":          func(d *grpcsrv.Deps) { d.Pages = nil },
		"no mutator":        func(d *grpcsrv.Deps) { d.Mutator = nil },
		"no member service": func(d *grpcsrv.Deps) { d.MemberService = nil },
		"no room service":   func(d *grpcsrv.Deps) { d.RoomService = nil },
		"no edits":          func(d *grpcsrv.Deps) { d.Edits = nil },
		"no hidden":         func(d *grpcsrv.Deps) { d.Hidden = nil },
		"no bookmarks":      func(d *grpcsrv.Deps) { d.Bookmarks = nil },
		"no replies":        func(d *grpcsrv.Deps) { d.Replies = nil },
		"no reader marks":   func(d *grpcsrv.Deps) { d.Mine = nil },
		"no room list":      func(d *grpcsrv.Deps) { d.RoomList = nil },
		"no threads":        func(d *grpcsrv.Deps) { d.Threads = nil },
		"no reactors":       func(d *grpcsrv.Deps) { d.Reactors = nil },
		"no pin board":      func(d *grpcsrv.Deps) { d.PinBoard = nil },
		"no member list":    func(d *grpcsrv.Deps) { d.MemberList = nil },
	} {
		deps := full
		drop(&deps)
		if _, err := grpcsrv.New(deps, quiet); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := grpcsrv.New(full, nil); err != nil {
		t.Errorf("New with defaults = %v, want nil", err)
	}
}
