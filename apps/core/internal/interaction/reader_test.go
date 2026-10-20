package interaction_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func (rg *rig) readerDeps(t *testing.T) interaction.ReaderDeps {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	views, err := message.NewReader(message.ReaderDeps{Access: checker, Pages: rg.msgs, Edits: memstore.NewEdits(), Hidden: memstore.NewHidden(), Interactions: rg.reactions})
	if err != nil {
		t.Fatalf("message.NewReader: %v", err)
	}
	projector, err := pinproj.New(rg.pins, rg.rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	return interaction.ReaderDeps{
		Access: checker, Messages: rg.msgs, Views: views, Interactions: rg.reactions, Threads: memstore.NewReplyThreads(), Pins: projector, Settings: rg.m,
	}
}

func TestNewReaderRequiresEveryDependency(t *testing.T) {
	rg := newRig(t, nil)
	full := rg.readerDeps(t)
	want := "invalid argument: interaction reader needs access, messages, message views, interactions, threads, pins and reaction settings"
	for name, drop := range map[string]func(d *interaction.ReaderDeps){
		"no access":       func(d *interaction.ReaderDeps) { d.Access = nil },
		"no messages":     func(d *interaction.ReaderDeps) { d.Messages = nil },
		"no views":        func(d *interaction.ReaderDeps) { d.Views = nil },
		"no interactions": func(d *interaction.ReaderDeps) { d.Interactions = nil },
		"no threads":      func(d *interaction.ReaderDeps) { d.Threads = nil },
		"no pins":         func(d *interaction.ReaderDeps) { d.Pins = nil },
		"no settings":     func(d *interaction.ReaderDeps) { d.Settings = nil },
	} {
		d := full
		drop(&d)
		_, err := interaction.NewReader(d)
		if !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewReader = %v, want ErrInvalidArgument", name, err)
		}
		if err.Error() != want {
			t.Fatalf("%s: NewReader = %q, want %q", name, err, want)
		}
	}
	if _, err := interaction.NewReader(full); err != nil {
		t.Fatalf("NewReader with every dep: %v", err)
	}
}
