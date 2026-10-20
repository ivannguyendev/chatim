package message_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func (rg *rig) readerDeps(t *testing.T) message.ReaderDeps {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	return message.ReaderDeps{Access: checker, Pages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Interactions: rg.reactions}
}

func TestNewReaderRequiresEveryDependency(t *testing.T) {
	rg := newRig(t, nil)
	full := rg.readerDeps(t)
	want := "invalid argument: message reader needs access, pages, edits, hidden and interactions"
	for name, drop := range map[string]func(d *message.ReaderDeps){
		"no access":       func(d *message.ReaderDeps) { d.Access = nil },
		"no pages":        func(d *message.ReaderDeps) { d.Pages = nil },
		"no edits":        func(d *message.ReaderDeps) { d.Edits = nil },
		"no hidden":       func(d *message.ReaderDeps) { d.Hidden = nil },
		"no interactions": func(d *message.ReaderDeps) { d.Interactions = nil },
	} {
		d := full
		drop(&d)
		_, err := message.NewReader(d)
		if !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewReader = %v, want ErrInvalidArgument", name, err)
		}
		if err.Error() != want {
			t.Fatalf("%s: NewReader = %q, want %q", name, err, want)
		}
	}
	if _, err := message.NewReader(full); err != nil {
		t.Fatalf("NewReader with every dep: %v", err)
	}
}
