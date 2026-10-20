package grpcsrv_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/message"
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
	reader := newReader(t, rg, o, pages)
	interactions := newInteractionService(t, rg, o, board)
	return grpcsrv.Deps{
		Sender: o.sender, Editor: newEditor(t, rg, o), Reader: reader,
		InteractionService: interactions, InteractionReader: newInteractionReader(t, rg, o, pages, reader, interactions, board),
		MemberService: members, MemberReader: newMemberReader(t, rg, o), RoomService: newRoomService(t, rg, o, members), RoomList: newRoomLister(t, rg),
	}
}

func TestNewRequiresEveryDependency(t *testing.T) {
	rg := memStores()
	full := depsOf(t, rg, options{sender: &fakeSender{}})
	for name, drop := range map[string]func(d *grpcsrv.Deps){
		"no sender":              func(d *grpcsrv.Deps) { d.Sender = nil },
		"no message editor":      func(d *grpcsrv.Deps) { d.Editor = nil },
		"no message reader":      func(d *grpcsrv.Deps) { d.Reader = nil },
		"no interaction service": func(d *grpcsrv.Deps) { d.InteractionService = nil },
		"no interaction reader":  func(d *grpcsrv.Deps) { d.InteractionReader = nil },
		"no member service":      func(d *grpcsrv.Deps) { d.MemberService = nil },
		"no member reader":       func(d *grpcsrv.Deps) { d.MemberReader = nil },
		"no room service":        func(d *grpcsrv.Deps) { d.RoomService = nil },
		"no room list":           func(d *grpcsrv.Deps) { d.RoomList = nil },
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

func newInteractionReader(t *testing.T, rg *rig, o options, pages message.Pages, views *message.Reader, settings *interaction.Service, board *pinproj.Projector) *interaction.Reader {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	r, err := interaction.NewReader(interaction.ReaderDeps{
		Access: checker, Messages: pages, Views: views, Interactions: rg.reactions, Threads: rg.threads, Pins: board, Settings: settings,
	})
	if err != nil {
		t.Fatalf("interaction.NewReader: %v", err)
	}
	return r
}

func newMemberReader(t *testing.T, rg *rig, o options) *member.Reader {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	r, err := member.NewReader(member.ReaderDeps{Access: checker, Members: rg.rooms})
	if err != nil {
		t.Fatalf("member.NewReader: %v", err)
	}
	return r
}
