package store

import (
	"math"
	"math/bits"
	"time"

	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (t StateTarget) Validate() error {
	switch {
	case !t.Collection.Valid():
		return invalid("state collection")
	case bits.OnesCount32(uint32(t.Bit)) != 1 || t.Bit&t.Collection.Bits() == 0:
		return invalid("state bit")
	case t.Room == 0:
		return invalid("state room")
	}
	return t.validateKey()
}

func (t StateTarget) validateKey() error {
	switch t.Collection {
	case StateMessages, StatePins:
		return requireSeq(t.Seq)
	case StateEdits:
		if t.Ver == 0 || t.Ver > math.MaxUint32 {
			return invalid("state edit version")
		}
		return requireSeq(t.Seq)
	case StateInteractions:
		if t.Kind != keys.ReactionKind && t.Kind != keys.BookmarkKind {
			return invalid("state interaction kind")
		}
		return firstError(requireSeq(t.Seq), requireUser(t.User, keys.MaxInteractionUser))
	case StateMembers:
		return requireUser(t.User, keys.MaxMemberUser)
	case StateHidden:
		return firstError(requireSeq(t.Seq), requireUser(t.User, keys.MaxMemberUser))
	default:
		return nil
	}
}

func ValidateStateTargets(targets []StateTarget) error {
	for _, t := range targets {
		if err := t.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func ValidateMarkTargets(c StateCollection, targets []StateTarget) error {
	for _, t := range targets {
		if t.Collection != c {
			return invalid("state collection")
		}
	}
	return ValidateStateTargets(targets)
}

func ValidatePendingScan(c StateCollection, limit int) error {
	return firstError(ValidateStateCollection(c), ValidateLimit(limit, MaxPendingPage))
}

func ValidateStateCollection(c StateCollection) error {
	if !c.Valid() {
		return invalid("state collection")
	}
	return nil
}

func ValidateCheckpoint(c StateCollection, at time.Time) error {
	return firstError(ValidateStateCollection(c), ValidateMarkTime(at))
}

func requireSeq(seq uint64) error {
	if seq == 0 {
		return invalid("state seq")
	}
	return nil
}

func requireUser(user string, maxLen int) error {
	if user == "" || len(user) > maxLen {
		return invalid("state user")
	}
	return nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
