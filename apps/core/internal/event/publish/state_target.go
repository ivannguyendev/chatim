package publish

import (
	"strconv"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func StateTargetsOf(ev *chatimv1.Event) []store.StateTarget {
	room, err := strconv.ParseUint(ev.GetRoomId(), 10, 64)
	if err != nil {
		return nil
	}
	if rp, ok := ev.GetPayload().(*chatimv1.Event_ReadPositions); ok {
		return readPositionTargets(room, rp.ReadPositions.GetEntries())
	}
	t, ok := messageStateTarget(ev)
	if !ok {
		t, ok = memberStateTarget(ev)
	}
	if !ok {
		t, ok = roomStateTarget(ev)
	}
	if !ok {
		return nil
	}
	t.Room = room
	if t.Validate() != nil {
		return nil
	}
	return []store.StateTarget{t}
}

func readPositionTargets(room uint64, entries []*chatimv1.ReadEntry) []store.StateTarget {
	out := make([]store.StateTarget, 0, len(entries))
	for _, e := range entries {
		t := store.StateTarget{Collection: store.StateMembers, Bit: store.BitReadPosition, Room: room, User: e.GetUser(), Ver: e.GetReadVer()}
		if t.Validate() == nil {
			out = append(out, t)
		}
	}
	return out
}

func messageStateTarget(ev *chatimv1.Event) (store.StateTarget, bool) {
	thread, seq := ev.GetThreadRoot(), ev.GetSeq()
	switch p := ev.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		return store.StateTarget{Collection: store.StateMessages, Bit: store.BitMsgCreated, Thread: thread, Seq: seq}, true
	case *chatimv1.Event_CountsChanged:
		return countsStateTarget(thread, seq, p.CountsChanged)
	case *chatimv1.Event_MessageEdited:
		return editStateTarget(thread, seq, p.MessageEdited.GetVer()), true
	case *chatimv1.Event_MessageDeleted:
		return editStateTarget(thread, seq, p.MessageDeleted.GetVer()), true
	case *chatimv1.Event_ReactionChanged:
		r := p.ReactionChanged
		return interactionStateTarget(thread, seq, keys.ReactionKind, r.GetUser(), r.GetChange()), true
	case *chatimv1.Event_BookmarkChanged:
		b := p.BookmarkChanged
		return interactionStateTarget(b.GetThreadRoot(), b.GetSeq(), keys.BookmarkKind, b.GetUser(), b.GetVer()), true
	case *chatimv1.Event_MessagePinned:
		return pinStateTarget(p.MessagePinned.GetPinVer()), true
	case *chatimv1.Event_MessageUnpinned:
		return pinStateTarget(p.MessageUnpinned.GetPinVer()), true
	default:
		return store.StateTarget{}, false
	}
}

func memberStateTarget(ev *chatimv1.Event) (store.StateTarget, bool) {
	switch p := ev.GetPayload().(type) {
	case *chatimv1.Event_MemberAdded:
		return memberSetTarget(p.MemberAdded.GetUser(), p.MemberAdded.GetVer()), true
	case *chatimv1.Event_MemberRemoved:
		return memberSetTarget(p.MemberRemoved.GetUser(), p.MemberRemoved.GetVer()), true
	case *chatimv1.Event_MemberRoleChanged:
		return memberSetTarget(p.MemberRoleChanged.GetUser(), p.MemberRoleChanged.GetVer()), true
	case *chatimv1.Event_MemberPriorityChanged:
		return memberSetTarget(p.MemberPriorityChanged.GetUser(), p.MemberPriorityChanged.GetVer()), true
	case *chatimv1.Event_ReadUpdated:
		r := p.ReadUpdated
		return store.StateTarget{Collection: store.StateMembers, Bit: store.BitReadPosition, User: r.GetUser(), Ver: r.GetReadVer()}, true
	case *chatimv1.Event_HistoryCleared:
		h := p.HistoryCleared
		if h.GetClearedAt() == nil {
			return store.StateTarget{}, false
		}
		return store.StateTarget{Collection: store.StateMembers, Bit: store.BitHistoryCleared, User: h.GetUser(), At: h.GetClearedAt().AsTime()}, true
	case *chatimv1.Event_RoomPreferencesChanged:
		r := p.RoomPreferencesChanged
		return store.StateTarget{Collection: store.StateMembers, Bit: store.BitPreferences, User: r.GetUser(), Ver: r.GetState().GetPreferenceVer()}, true
	case *chatimv1.Event_MessageHidden:
		h := p.MessageHidden
		return store.StateTarget{Collection: store.StateHidden, Bit: store.BitHidden, User: h.GetUser(), Thread: h.GetThreadRoot(), Seq: h.GetSeq()}, true
	default:
		return store.StateTarget{}, false
	}
}

func roomStateTarget(ev *chatimv1.Event) (store.StateTarget, bool) {
	switch p := ev.GetPayload().(type) {
	case *chatimv1.Event_RoomCreated:
		return store.StateTarget{Collection: store.StateRooms, Bit: store.BitRoomCreated}, true
	case *chatimv1.Event_MemberCountChanged:
		return store.StateTarget{Collection: store.StateRooms, Bit: store.BitMemberCount, Ver: p.MemberCountChanged.GetMemberCountVer()}, true
	case *chatimv1.Event_RoomUpdated:
		return store.StateTarget{Collection: store.StateRooms, Bit: store.BitRoomInfo, Ver: p.RoomUpdated.GetRoom().GetInfoVer()}, true
	default:
		return store.StateTarget{}, false
	}
}

func countsStateTarget(thread, seq uint64, c *chatimv1.CountsChanged) (store.StateTarget, bool) {
	t := store.StateTarget{Collection: store.StateMessages, Thread: thread, Seq: seq}
	switch c.GetCounter() {
	case pbconv.ReactionsCounter:
		t.Bit, t.Ver = store.BitReactionCounts, c.GetReactions().GetVer()
	case pbconv.RepliesCounter:
		t.Bit, t.Ver = store.BitReplyCounts, c.GetReplyCount().GetVer()
	default:
		return store.StateTarget{}, false
	}
	return t, true
}

func editStateTarget(thread, seq uint64, ver uint32) store.StateTarget {
	return store.StateTarget{Collection: store.StateEdits, Bit: store.BitEditFact, Thread: thread, Seq: seq, Ver: uint64(ver)}
}

func interactionStateTarget(thread, seq uint64, kind keys.InteractionKind, user string, ver uint32) store.StateTarget {
	return store.StateTarget{Collection: store.StateInteractions, Bit: store.BitInteraction, Thread: thread, Seq: seq, User: user, Kind: kind, Ver: uint64(ver)}
}

func pinStateTarget(pinVer uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StatePins, Bit: store.BitPinFact, Seq: pinVer}
}

func memberSetTarget(user string, ver uint32) store.StateTarget {
	return store.StateTarget{Collection: store.StateMembers, Bit: store.BitMember, User: user, Ver: uint64(ver)}
}
