package domain

import (
	"time"

	"github.com/ivannguyendev/chatim/pkg/ids"
)

type State uint8

const (
	StateLive    State = 1
	StateRetired State = 2
)

type Mention struct {
	Key        MessageKey
	Tenant     string
	Target     Target
	Sender     string
	State      State
	MessageVer uint32
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Plan struct {
	Key     MessageKey
	Tenant  string
	Sender  string
	Ver     uint32
	Live    []Target
	Retired []Target
}

func (p Plan) Validate() error {
	if err := p.Key.Validate(); err != nil {
		return err
	}
	if err := ids.ValidTenant(p.Tenant); err != nil {
		return err
	}
	if err := ids.ValidUser(p.Sender); err != nil {
		return err
	}
	seen := make(map[Target]struct{}, len(p.Live)+len(p.Retired))
	if err := checkTargets(p.Live, seen, true); err != nil {
		return err
	}
	return checkTargets(p.Retired, seen, false)
}

func (p Plan) InMillis() Plan {
	p.Live, p.Retired = targetsInMillis(p.Live), targetsInMillis(p.Retired)
	return p
}

func targetsInMillis(targets []Target) []Target {
	if targets == nil {
		return nil
	}
	out := make([]Target, len(targets))
	for i, t := range targets {
		t.At = t.At.Truncate(time.Millisecond)
		out[i] = t
	}
	return out
}

func checkTargets(targets []Target, seen map[Target]struct{}, timed bool) error {
	for _, t := range targets {
		if err := t.Validate(); err != nil {
			return err
		}
		if timed && t.At.IsZero() {
			return invalid("mention target time")
		}
		if _, dup := seen[t.Identity()]; dup {
			return invalid("mention plan targets")
		}
		seen[t.Identity()] = struct{}{}
	}
	return nil
}
