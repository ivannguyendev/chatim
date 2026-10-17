package store

import (
	"context"
	"strconv"
	"time"

	"github.com/ivannguyendev/chatim/pkg/keys"
)

const MaxPendingPage = 1000

type StateCollection uint8

const (
	StateMessages StateCollection = iota + 1
	StateEdits
	StateInteractions
	StatePins
	StateMembers
	StateHidden
	StateRooms
)

type StateBit uint32

const (
	BitMsgCreated     StateBit = 1
	BitReactionCounts StateBit = 2
	BitReplyCounts    StateBit = 4

	BitEditFact    StateBit = 1
	BitInteraction StateBit = 1
	BitPinFact     StateBit = 1
	BitHidden      StateBit = 1

	BitMember         StateBit = 1
	BitReadPosition   StateBit = 2
	BitHistoryCleared StateBit = 4
	BitPreferences    StateBit = 8

	BitRoomCreated StateBit = 1
	BitMemberCount StateBit = 2
	BitRoomInfo    StateBit = 4
)

var stateCollectionNames = [...]string{
	StateMessages:     "messages",
	StateEdits:        "message_edits",
	StateInteractions: "message_interactions",
	StatePins:         "pin_actions",
	StateMembers:      "members",
	StateHidden:       "hidden",
	StateRooms:        "rooms",
}

var stateCollectionBits = [...]StateBit{
	StateMessages:     BitMsgCreated | BitReactionCounts | BitReplyCounts,
	StateEdits:        BitEditFact,
	StateInteractions: BitInteraction,
	StatePins:         BitPinFact,
	StateMembers:      BitMember | BitReadPosition | BitHistoryCleared | BitPreferences,
	StateHidden:       BitHidden,
	StateRooms:        BitRoomCreated | BitMemberCount | BitRoomInfo,
}

func AllStateCollections() []StateCollection {
	return []StateCollection{StateMessages, StateEdits, StateInteractions, StatePins, StateMembers, StateHidden, StateRooms}
}

func (c StateCollection) Valid() bool { return c >= StateMessages && c <= StateRooms }

func (c StateCollection) String() string {
	if !c.Valid() {
		return "collection(" + strconv.Itoa(int(c)) + ")"
	}
	return stateCollectionNames[c]
}

func (c StateCollection) Bits() StateBit {
	if !c.Valid() {
		return 0
	}
	return stateCollectionBits[c]
}

type StateTarget struct {
	Collection        StateCollection
	Bit               StateBit
	Room, Thread, Seq uint64
	User              string
	Kind              keys.InteractionKind
	Ver               uint64
	At                time.Time
}

type PendingDoc struct {
	Collection        StateCollection
	Room, Thread, Seq uint64
	User              string
	Kind              keys.InteractionKind
	Ver               uint64
	State             StateBit
	StateAt           time.Time
}

type PendingCursor struct {
	At  time.Time
	Key []byte
}

func (c PendingCursor) IsZero() bool { return c.Key == nil }

type EventStates interface {
	Pending(ctx context.Context, c StateCollection, from, before time.Time, after PendingCursor, limit int) ([]PendingDoc, PendingCursor, error)
	Clear(ctx context.Context, targets []StateTarget) error
	Mark(ctx context.Context, c StateCollection, targets []StateTarget) error
}

type SweepCheckpoints interface {
	Get(ctx context.Context, c StateCollection) (time.Time, error)
	Advance(ctx context.Context, c StateCollection, at time.Time) error
}
