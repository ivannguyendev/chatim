package domain

import (
	"cmp"
	"slices"
	"time"
)

type PinOp uint8

const (
	PinOpPin PinOp = iota + 1
	PinOpUnpin
)

type PinAction struct {
	Room   uint64
	PV     uint64
	Tenant string
	Op     PinOp
	Thread uint64
	Seq    uint64
	By     string
	At     time.Time
}

type Pin struct {
	Thread uint64
	Seq    uint64
	By     string
	At     time.Time
	PV     uint64
}

type PinState struct {
	Pins    []Pin
	Version uint64
}

func (s PinState) Pinned(thread, seq uint64) bool {
	return slices.ContainsFunc(s.Pins, func(p Pin) bool { return p.Thread == thread && p.Seq == seq })
}

func FoldPins(s PinState, facts []PinAction) PinState {
	ordered := slices.SortedFunc(slices.Values(facts), func(a, b PinAction) int { return cmp.Compare(a.PV, b.PV) })
	out := PinState{Pins: slices.Clone(s.Pins), Version: s.Version}
	for _, a := range ordered {
		if a.PV <= out.Version {
			continue
		}
		out.Version = a.PV
		at := slices.IndexFunc(out.Pins, func(p Pin) bool { return p.Thread == a.Thread && p.Seq == a.Seq })
		switch {
		case a.Op == PinOpPin && at < 0:
			out.Pins = slices.Insert(out.Pins, 0, Pin{Thread: a.Thread, Seq: a.Seq, By: a.By, At: a.At, PV: a.PV})
		case a.Op == PinOpUnpin && at >= 0:
			out.Pins = slices.Delete(out.Pins, at, at+1)
		}
	}
	return out
}
