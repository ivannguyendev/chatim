package e2e

import (
	"strconv"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	KindBookmark   = "bookmark_changed"
	repliesCounter = "replies"
)

func RepliesEventID(room string, seq, version uint64) string {
	return MessageEventID(room, seq) + "-" + repliesCounter + "-v" + strconv.FormatUint(version, 10)
}

func BookmarkEventID(room string, seq uint64, user string, ver uint32) string {
	return room + "-bm-0-" + strconv.FormatUint(seq, 10) + "-" + user + "-v" + strconv.FormatUint(uint64(ver), 10)
}

func RepliesPayload(n uint32) string { return "replies=" + strconv.FormatUint(uint64(n), 10) }

func BookmarkPayload(on bool) string { return "on=" + strconv.FormatBool(on) }

func LinksPayload(m *chatimv1.Message) string {
	var parts []string
	if r := m.GetReplyTo(); r != nil {
		parts = append(parts, "reply_to="+strconv.FormatUint(r.GetSeq(), 10))
	}
	if f := m.GetForwardFrom(); f != nil {
		origin := f.GetRoomId() + "-" + strconv.FormatUint(f.GetThreadRoot(), 10) + "-" + strconv.FormatUint(f.GetSeq(), 10)
		parts = append(parts, "forward_from="+origin, "author="+f.GetAuthor())
	}
	if targets := m.GetMentionTargets(); len(targets) > 0 {
		names := make([]string, len(targets))
		for i, t := range targets {
			names[i] = mentionName(t)
		}
		parts = append(parts, "mentions="+strings.Join(names, ","))
	}
	if m.GetMentionAll() {
		parts = append(parts, "all")
	}
	return strings.Join(parts, " ")
}

func mentionName(t *chatimv1.MentionTarget) string {
	kind := strings.ToLower(strings.TrimPrefix(t.GetKind().String(), "MENTION_KIND_"))
	return kind + ":" + t.GetId()
}

func countsPayload(c *chatimv1.CountsChanged) string {
	if c.GetCounter() == repliesCounter {
		return RepliesPayload(c.GetReplyCount().GetCount())
	}
	return FormatCounts(c.GetReactions().GetCounts())
}

func bookmarkOf(subject string, ev *chatimv1.Event) (Event, bool) {
	b := ev.GetBookmarkChanged()
	if b == nil {
		return Event{}, false
	}
	return Event{
		Kind: KindBookmark, Room: ev.GetRoomId(), ID: ev.GetId(), Seq: b.GetSeq(), User: b.GetUser(),
		Version: b.GetVer(), Text: BookmarkPayload(b.GetOn()), Subject: subject,
	}, true
}
