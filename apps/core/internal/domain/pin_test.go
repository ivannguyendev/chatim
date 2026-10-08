package domain_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

var pinAt = time.Unix(1_700_000_000, 0).UTC()

func pinAction(pv uint64, op domain.PinOp, thread, seq uint64) domain.PinAction {
	return domain.PinAction{Room: 7, PV: pv, Tenant: "acme", Op: op, Thread: thread, Seq: seq, By: "alice", At: pinAt.Add(time.Duration(pv) * time.Second)}
}

func pinned(pv, thread, seq uint64) domain.Pin {
	a := pinAction(pv, domain.PinOpPin, thread, seq)
	return domain.Pin{Thread: thread, Seq: seq, By: a.By, At: a.At, PV: pv}
}

func TestPinOpsAreTheStoredValues(t *testing.T) {
	if domain.PinOpPin != 1 || domain.PinOpUnpin != 2 {
		t.Fatalf("PinOpPin = %d, PinOpUnpin = %d; want 1 and 2, the values stored in pin_actions.op", domain.PinOpPin, domain.PinOpUnpin)
	}
}

func TestFoldPins(t *testing.T) {
	tests := []struct {
		name  string
		state domain.PinState
		facts []domain.PinAction
		want  domain.PinState
	}{
		{
			"pins in pv order with the newest first",
			domain.PinState{},
			[]domain.PinAction{pinAction(2, domain.PinOpPin, 0, 5), pinAction(1, domain.PinOpPin, 0, 3)},
			domain.PinState{Pins: []domain.Pin{pinned(2, 0, 5), pinned(1, 0, 3)}, Version: 2},
		},
		{
			"unpin removes only that message",
			domain.PinState{Pins: []domain.Pin{pinned(2, 0, 5), pinned(1, 0, 3)}, Version: 2},
			[]domain.PinAction{pinAction(3, domain.PinOpUnpin, 0, 3)},
			domain.PinState{Pins: []domain.Pin{pinned(2, 0, 5)}, Version: 3},
		},
		{
			"facts at or below the version are skipped",
			domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 2},
			[]domain.PinAction{pinAction(2, domain.PinOpUnpin, 0, 3), pinAction(1, domain.PinOpPin, 0, 9), pinAction(3, domain.PinOpPin, 4, 3)},
			domain.PinState{Pins: []domain.Pin{pinned(3, 4, 3), pinned(1, 0, 3)}, Version: 3},
		},
		{
			"repeated pins and stray unpins only move the version",
			domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 1},
			[]domain.PinAction{pinAction(2, domain.PinOpPin, 0, 3), pinAction(3, domain.PinOpUnpin, 0, 9)},
			domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 3},
		},
		{
			"no facts keep the state",
			domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 1},
			nil,
			domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.FoldPins(tt.state, tt.facts)
			if got.Version != tt.want.Version || !slices.Equal(got.Pins, tt.want.Pins) {
				t.Fatalf("FoldPins = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFoldPinsLeavesItsInputsAlone(t *testing.T) {
	state := domain.PinState{Pins: []domain.Pin{pinned(2, 0, 4), pinned(1, 0, 3)}, Version: 2}
	facts := []domain.PinAction{pinAction(4, domain.PinOpUnpin, 0, 4), pinAction(3, domain.PinOpPin, 0, 8)}
	pins, order := slices.Clone(state.Pins), slices.Clone(facts)
	domain.FoldPins(state, facts)
	if !slices.Equal(state.Pins, pins) || !slices.Equal(facts, order) {
		t.Fatalf("FoldPins changed its inputs: pins %+v, facts %+v", state.Pins, facts)
	}
}

func TestPinStatePinned(t *testing.T) {
	s := domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 1}
	if !s.Pinned(0, 3) || s.Pinned(0, 4) || s.Pinned(1, 3) || (domain.PinState{}).Pinned(0, 3) {
		t.Fatalf("Pinned is wrong for %+v", s)
	}
}
