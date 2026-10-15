package store_test

import (
	"errors"
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestStateCollectionsNameEveryCollectionOnce(t *testing.T) {
	want := map[store.StateCollection]string{
		store.StateMessages: "messages", store.StateEdits: "message_edits", store.StateInteractions: "message_interactions",
		store.StatePins: "pin_actions", store.StateMembers: "members", store.StateHidden: "hidden", store.StateRooms: "rooms",
	}
	all := store.AllStateCollections()
	if len(all) != len(want) {
		t.Fatalf("AllStateCollections() = %v, want %d collections", all, len(want))
	}
	for _, c := range all {
		if c.String() != want[c] || !c.Valid() {
			t.Fatalf("collection %d = %q valid %v, want %q", c, c.String(), c.Valid(), want[c])
		}
	}
	if store.StateCollection(0).Valid() || store.StateCollection(8).Valid() {
		t.Fatal("collections outside the list are valid")
	}
}

func TestStateBitsFollowTheBitTable(t *testing.T) {
	want := map[store.StateCollection]store.StateBit{
		store.StateMessages:     store.BitMsgCreated | store.BitReactionCounts | store.BitReplyCounts,
		store.StateEdits:        store.BitEditFact,
		store.StateInteractions: store.BitInteraction,
		store.StatePins:         store.BitPinFact,
		store.StateMembers:      store.BitMember | store.BitReadPosition | store.BitHistoryCleared | store.BitPreferences,
		store.StateHidden:       store.BitHidden,
		store.StateRooms:        store.BitRoomCreated | store.BitMemberCount | store.BitRoomInfo,
	}
	for c, bits := range want {
		if c.Bits() != bits {
			t.Fatalf("%s bits = %b, want %b", c, c.Bits(), bits)
		}
	}
	if store.BitReadPosition != 2 || store.BitHistoryCleared != 4 || store.BitPreferences != 8 || store.BitRoomInfo != 4 || store.BitReplyCounts != 4 {
		t.Fatal("bit values differ from the bit table")
	}
}

func validStateTargets() map[string]store.StateTarget {
	return map[string]store.StateTarget{
		"message":     {Collection: store.StateMessages, Bit: store.BitReactionCounts, Room: 9, Thread: 0, Seq: 4, Ver: 2},
		"edit":        {Collection: store.StateEdits, Bit: store.BitEditFact, Room: 9, Seq: 4, Ver: 1},
		"reaction":    {Collection: store.StateInteractions, Bit: store.BitInteraction, Room: 9, Seq: 4, User: "bob", Kind: keys.ReactionKind, Ver: 1},
		"bookmark":    {Collection: store.StateInteractions, Bit: store.BitInteraction, Room: 9, Seq: 4, User: "bob", Kind: keys.BookmarkKind, Ver: 1},
		"pin":         {Collection: store.StatePins, Bit: store.BitPinFact, Room: 9, Seq: 3},
		"member":      {Collection: store.StateMembers, Bit: store.BitPreferences, Room: 9, User: "bob", Ver: 2},
		"hidden":      {Collection: store.StateHidden, Bit: store.BitHidden, Room: 9, Thread: 7, Seq: 4, User: "bob"},
		"room":        {Collection: store.StateRooms, Bit: store.BitRoomInfo, Room: 9, Ver: 3},
		"room create": {Collection: store.StateRooms, Bit: store.BitRoomCreated, Room: 9},
	}
}

func TestStateTargetValidateAcceptsEveryCollection(t *testing.T) {
	for name, target := range validStateTargets() {
		if err := target.Validate(); err != nil {
			t.Fatalf("%s: Validate(%+v) = %v", name, target, err)
		}
	}
}

func TestStateTargetValidateRejectsBadTargets(t *testing.T) {
	ok := validStateTargets()
	bad := map[string]store.StateTarget{
		"unknown collection":   withTarget(ok["room"], func(x *store.StateTarget) { x.Collection = 0 }),
		"no bit":               withTarget(ok["room"], func(x *store.StateTarget) { x.Bit = 0 }),
		"two bits":             withTarget(ok["room"], func(x *store.StateTarget) { x.Bit = store.BitRoomCreated | store.BitRoomInfo }),
		"bit of no collection": withTarget(ok["message"], func(x *store.StateTarget) { x.Bit = 8 }),
		"edit bit 2":           withTarget(ok["edit"], func(x *store.StateTarget) { x.Bit = 2 }),
		"room 0":               withTarget(ok["room"], func(x *store.StateTarget) { x.Room = 0 }),
		"message seq 0":        withTarget(ok["message"], func(x *store.StateTarget) { x.Seq = 0 }),
		"edit ver 0":           withTarget(ok["edit"], func(x *store.StateTarget) { x.Ver = 0 }),
		"edit ver too big":     withTarget(ok["edit"], func(x *store.StateTarget) { x.Ver = math.MaxUint32 + 1 }),
		"reaction no user":     withTarget(ok["reaction"], func(x *store.StateTarget) { x.User = "" }),
		"reaction long user":   withTarget(ok["reaction"], func(x *store.StateTarget) { x.User = string(make([]byte, keys.MaxInteractionUser+1)) }),
		"reply kind":           withTarget(ok["reaction"], func(x *store.StateTarget) { x.Kind = keys.ReplyKind }),
		"interaction seq 0":    withTarget(ok["bookmark"], func(x *store.StateTarget) { x.Seq = 0 }),
		"pin version 0":        withTarget(ok["pin"], func(x *store.StateTarget) { x.Seq = 0 }),
		"member no user":       withTarget(ok["member"], func(x *store.StateTarget) { x.User = "" }),
		"member long user":     withTarget(ok["member"], func(x *store.StateTarget) { x.User = string(make([]byte, keys.MaxMemberUser+1)) }),
		"hidden no user":       withTarget(ok["hidden"], func(x *store.StateTarget) { x.User = "" }),
		"hidden seq 0":         withTarget(ok["hidden"], func(x *store.StateTarget) { x.Seq = 0 }),
		"count ver too big":    withTarget(ok["message"], func(x *store.StateTarget) { x.Ver = math.MaxUint32 + 1 }),
		"reaction ver too big": withTarget(ok["reaction"], func(x *store.StateTarget) { x.Ver = math.MaxUint32 + 1 }),
		"member ver too big":   withTarget(ok["member"], func(x *store.StateTarget) { x.Bit, x.Ver = store.BitMember, math.MaxUint32+1 }),
		"read ver too big":     withTarget(ok["member"], func(x *store.StateTarget) { x.Bit, x.Ver = store.BitReadPosition, math.MaxUint32+1 }),
		"pref ver too big":     withTarget(ok["member"], func(x *store.StateTarget) { x.Ver = math.MaxInt64 + 1 }),
		"room info too big":    withTarget(ok["room"], func(x *store.StateTarget) { x.Ver = math.MaxUint32 + 1 }),
		"room count too big":   withTarget(ok["room"], func(x *store.StateTarget) { x.Bit, x.Ver = store.BitMemberCount, math.MaxUint32+1 }),
	}
	for name, target := range bad {
		if err := target.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: Validate(%+v) = %v, want invalid argument", name, target, err)
		}
	}
}

func TestStateTargetValidateAcceptsTheLargestVersion(t *testing.T) {
	for name, target := range validStateTargets() {
		target.Ver = math.MaxUint32
		if err := target.Validate(); err != nil {
			t.Fatalf("%s: Validate(ver MaxUint32) = %v", name, err)
		}
	}
}

func TestStateTargetValidateIgnoresTheVersionOfInsertOnceBits(t *testing.T) {
	for name, target := range map[string]store.StateTarget{
		"message":       {Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: 9, Seq: 4},
		"pin":           {Collection: store.StatePins, Bit: store.BitPinFact, Room: 9, Seq: 3},
		"hidden":        {Collection: store.StateHidden, Bit: store.BitHidden, Room: 9, Seq: 4, User: "bob"},
		"room create":   {Collection: store.StateRooms, Bit: store.BitRoomCreated, Room: 9},
		"history clear": {Collection: store.StateMembers, Bit: store.BitHistoryCleared, Room: 9, User: "bob"},
	} {
		target.Ver = math.MaxUint64
		if err := target.Validate(); err != nil {
			t.Fatalf("%s: Validate(unused ver) = %v", name, err)
		}
	}
}

func TestPendingCursorZeroMeansNoMorePages(t *testing.T) {
	if !(store.PendingCursor{}).IsZero() {
		t.Fatal("empty cursor is not zero")
	}
	if (store.PendingCursor{Key: []byte{1}}).IsZero() {
		t.Fatal("cursor with a key is zero")
	}
}

func withTarget(x store.StateTarget, change func(*store.StateTarget)) store.StateTarget {
	change(&x)
	return x
}
