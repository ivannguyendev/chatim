package message_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNewRequiresEveryDependency(t *testing.T) {
	rg := newRig(t, nil)
	full := rg.deps(t, nil)
	full.Now = nil
	want := "invalid argument: message editor needs access, messages, edits, hidden, events and interactions"
	for name, drop := range map[string]func(d *message.Deps){
		"no access":       func(d *message.Deps) { d.Access = nil },
		"no messages":     func(d *message.Deps) { d.Messages = nil },
		"no edits":        func(d *message.Deps) { d.Edits = nil },
		"no hidden":       func(d *message.Deps) { d.Hidden = nil },
		"no events":       func(d *message.Deps) { d.Events = nil },
		"no interactions": func(d *message.Deps) { d.Interactions = nil },
	} {
		d := full
		drop(&d)
		_, err := message.New(d)
		if !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
		if err.Error() != want {
			t.Fatalf("%s: New = %q, want %q", name, err, want)
		}
	}
	if _, err := message.New(full); err != nil {
		t.Fatalf("New with a default clock: %v", err)
	}
}

func TestNewRefusesMentionTargetsOutOfBoundsWithTheLimitsError(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	d.Limits = message.Limits{MentionTargets: -3}
	_, err := message.New(d)
	want := d.Limits.Validate()
	if err == nil || want == nil || err.Error() != want.Error() || !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New = %v, want the Limits error %v", err, want)
	}
	if err.Error() != "invalid argument: mention targets -3 must be at least 1" {
		t.Fatalf("New = %q, want the mention targets bound", err)
	}
}
