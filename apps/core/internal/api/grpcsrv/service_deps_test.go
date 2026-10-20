package grpcsrv_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func depsOf(t *testing.T, rg *rig, o options) grpcsrv.Deps {
	t.Helper()
	board, err := pinproj.New(rg.pins, rg.rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	members := newMemberService(t, rg, o)
	pages := o.pageReader(rg.msgs)
	return grpcsrv.Deps{
		Sender: o.sender, Rooms: rg.rooms, Pages: pages, Policy: o.policy,
		Editor: newEditor(t, rg, o), Reader: newReader(t, rg, o, pages), InteractionService: newInteractionService(t, rg, o, board), MemberService: members, RoomService: newRoomService(t, rg, o, members), Bookmarks: rg.reactions, Replies: rg.reactions,
		RoomList: newRoomLister(t, rg),
		Threads:  rg.threads, Reactors: rg.reactions, PinBoard: board, MemberList: rg.rooms,
	}
}

func TestNewRequiresEveryDependency(t *testing.T) {
	rg := memStores()
	full := depsOf(t, rg, options{sender: &fakeSender{}})
	for name, drop := range map[string]func(d *grpcsrv.Deps){
		"no sender":              func(d *grpcsrv.Deps) { d.Sender = nil },
		"no rooms":               func(d *grpcsrv.Deps) { d.Rooms = nil },
		"no pages":               func(d *grpcsrv.Deps) { d.Pages = nil },
		"no message editor":      func(d *grpcsrv.Deps) { d.Editor = nil },
		"no message reader":      func(d *grpcsrv.Deps) { d.Reader = nil },
		"no interaction service": func(d *grpcsrv.Deps) { d.InteractionService = nil },
		"no member service":      func(d *grpcsrv.Deps) { d.MemberService = nil },
		"no room service":        func(d *grpcsrv.Deps) { d.RoomService = nil },
		"no bookmarks":           func(d *grpcsrv.Deps) { d.Bookmarks = nil },
		"no replies":             func(d *grpcsrv.Deps) { d.Replies = nil },
		"no room list":           func(d *grpcsrv.Deps) { d.RoomList = nil },
		"no threads":             func(d *grpcsrv.Deps) { d.Threads = nil },
		"no reactors":            func(d *grpcsrv.Deps) { d.Reactors = nil },
		"no pin board":           func(d *grpcsrv.Deps) { d.PinBoard = nil },
		"no member list":         func(d *grpcsrv.Deps) { d.MemberList = nil },
	} {
		deps := full
		drop(&deps)
		if _, err := grpcsrv.New(deps); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := grpcsrv.New(full); err != nil {
		t.Errorf("New with every dependency = %v, want nil", err)
	}
}

func newInteractionService(t *testing.T, rg *rig, o options, projector *pinproj.Projector) *interaction.Service {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	var events interaction.EventPublisher = nopPublisher{}
	if o.events != nil {
		events = o.events
	}
	s, err := interaction.New(interaction.Deps{
		Access: checker, Messages: rg.msgs, Interactions: rg.reactions, Counts: rg.msgs, CountTimers: nopTimers{},
		Pins: rg.pins, Projector: projector, Events: events, Limits: o.limits, Now: o.now,
	})
	if err != nil {
		t.Fatalf("interaction.New: %v", err)
	}
	return s
}
