package e2e

import (
	"strconv"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	KindReaction = "reaction_changed"
	KindCounts   = "counts_changed"
	KindPinned   = "msg_pinned"
	KindUnpinned = "msg_unpinned"
)

type Reaction struct {
	Seq     uint64 `json:"seq"`
	Emoji   string `json:"emoji"`
	Change  uint32 `json:"change"`
	Version uint64 `json:"version"`
}

type Pin struct {
	Seq     uint64 `json:"seq"`
	Version uint64 `json:"version"`
}

func ReactionEventID(room string, seq uint64, user string, change uint32) string {
	return MessageEventID(room, seq) + "-" + user + "-n" + strconv.FormatUint(uint64(change), 10)
}

func CountsEventID(room string, seq, version uint64) string {
	return MessageEventID(room, seq) + "-reactions-v" + strconv.FormatUint(version, 10)
}

func PinEventID(room string, version uint64) string {
	return room + "-p" + strconv.FormatUint(version, 10)
}

func FormatCounts(counts []*chatimv1.ReactionCount) string {
	parts := make([]string, len(counts))
	for i, c := range counts {
		parts[i] = c.GetEmoji() + "=" + strconv.FormatUint(uint64(c.GetCount()), 10)
	}
	return strings.Join(parts, ",")
}

func (e Event) IsMark() bool {
	switch e.Kind {
	case KindReaction, KindCounts, KindPinned, KindUnpinned:
		return true
	default:
		return false
	}
}

func markOf(subject string, ev *chatimv1.Event) (Event, bool) {
	out := Event{Room: ev.GetRoomId(), ID: ev.GetId(), Seq: ev.GetSeq(), Subject: subject}
	switch {
	case ev.GetReactionChanged() != nil:
		r := ev.GetReactionChanged()
		out.Kind, out.User, out.Text, out.Version = KindReaction, r.GetUser(), r.GetEmoji(), r.GetChange()
	case ev.GetCountsChanged() != nil:
		out.Kind, out.Text = KindCounts, countsPayload(ev.GetCountsChanged())
	case ev.GetMessagePinned() != nil:
		out.Kind, out.CID = KindPinned, ev.GetMessagePinned().GetMessage().GetCid()
	case ev.GetMessageUnpinned() != nil:
		out.Kind, out.CID = KindUnpinned, ev.GetMessageUnpinned().GetMessage().GetCid()
	default:
		return Event{}, false
	}
	return out, true
}
