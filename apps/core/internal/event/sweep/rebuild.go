package sweep

import (
	"context"
	"errors"
	"math/bits"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Rebuilder interface {
	Rebuild(ctx context.Context, doc store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error)
}

type dropError struct {
	target store.StateTarget
	err    error
}

func (e *dropError) Error() string { return e.err.Error() }

func (e *dropError) Unwrap() error { return e.err }

func dropWith(target store.StateTarget, err error) error {
	return &dropError{target: target, err: err}
}

var errUnknownVersion = errors.New("the gone document's version is unknown; the flag stays")

func dropTarget(doc store.PendingDoc, bit store.StateBit, err error) (store.StateTarget, bool) {
	if d, ok := errors.AsType[*dropError](err); ok {
		return d.target, true
	}
	return store.StateTarget{
		Collection: doc.Collection, Bit: bit, Room: doc.Room, Thread: doc.Thread, Seq: doc.Seq,
		User: doc.User, Kind: doc.Kind, Ver: doc.Ver,
	}, versionInKey(doc.Collection, bit)
}

func versionInKey(c store.StateCollection, bit store.StateBit) bool {
	switch c {
	case store.StateMessages:
		return bit == store.BitMsgCreated
	case store.StateRooms:
		return bit == store.BitRoomCreated
	case store.StateEdits, store.StatePins, store.StateHidden:
		return true
	default:
		return false
	}
}

func setBits(state store.StateBit) []store.StateBit {
	out := make([]store.StateBit, 0, bits.OnesCount32(uint32(state)))
	for rest := uint32(state); rest != 0; rest &= rest - 1 {
		out = append(out, store.StateBit(1)<<bits.TrailingZeros32(rest))
	}
	return out
}

func one(ev *chatimv1.Event) []*chatimv1.Event { return []*chatimv1.Event{ev} }
