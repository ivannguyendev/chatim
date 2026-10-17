package e2e

import (
	"fmt"
	"strconv"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	KindNotified  = "mention_notified"
	KindRetracted = "mention_retracted"
	AllTarget     = "all"
	ReasonEdited  = "edited"
	ReasonDeleted = "deleted"
)

type Notice struct {
	ID      string
	Kind    string
	User    string
	Room    string
	Seq     uint64
	Detail  string
	Subject string
}

func NotifySubject(root, tenant, user string) string {
	return root + "." + tenant + "." + user + ".mention"
}

func NotifiedID(room string, seq uint64, user string, ver uint32) string {
	return noticeID(room, seq, user, "m", ver)
}

func RetractedID(room string, seq uint64, user string, ver uint32) string {
	return noticeID(room, seq, user, "r", ver)
}

func noticeID(room string, seq uint64, user, mark string, ver uint32) string {
	return MessageEventID(room, seq) + "-" + user + "-" + mark + strconv.FormatUint(uint64(ver), 10)
}

func UserTarget(id string) string { return "user:" + id }

func GroupTarget(id string) string { return "group:" + id }

func NotifiedBy(room string, seq uint64, user string, ver uint32, target string) Notice {
	return Notice{ID: NotifiedID(room, seq, user, ver), Kind: KindNotified, User: user, Room: room, Seq: seq, Detail: target}
}

func RetractedFor(room string, seq uint64, user string, ver uint32, reason string) Notice {
	return Notice{ID: RetractedID(room, seq, user, ver), Kind: KindRetracted, User: user, Room: room, Seq: seq, Detail: reason}
}

func NoticeOf(subject string, n *chatimv1.Notification) (Notice, bool) {
	out := Notice{ID: n.GetId(), User: n.GetUser(), Room: n.GetRoomId(), Seq: n.GetSeq(), Subject: subject}
	switch p := n.GetPayload().(type) {
	case *chatimv1.Notification_MentionNotified:
		out.Kind, out.Detail = KindNotified, targetName(p.MentionNotified.GetTarget())
		if m := p.MentionNotified.GetMessage(); m.GetRoomId() != out.Room || m.GetSeq() != out.Seq {
			out.Detail = fmt.Sprintf("snapshot of room %s seq %d", m.GetRoomId(), m.GetSeq())
		}
	case *chatimv1.Notification_MentionRetracted:
		out.Kind = KindRetracted
		out.Detail = strings.ToLower(strings.TrimPrefix(p.MentionRetracted.GetReason().String(), "RETRACT_REASON_"))
	default:
		return Notice{}, false
	}
	return out, true
}

func targetName(t *chatimv1.MentionTarget) string {
	if t.GetKind() == chatimv1.MentionKind_MENTION_KIND_ALL {
		return AllTarget
	}
	return mentionName(t)
}

func CheckNotices(room string, wants, got []Notice) ([]string, error) {
	byID := make(map[string]Notice, len(wants))
	for _, w := range wants {
		byID[w.ID] = w
	}
	seen := make(map[string]bool, len(wants))
	for _, n := range got {
		if n.Room != room {
			continue
		}
		w, ok := byID[n.ID]
		switch {
		case !ok:
			return nil, fmt.Errorf("unexpected %s %s (%s) on %s", n.Kind, n.ID, n.Detail, n.Subject)
		case n != w:
			return nil, fmt.Errorf("notice %s is %+v, want %+v", n.ID, n, w)
		}
		seen[n.ID] = true
	}
	var missing []string
	for _, w := range wants {
		if !seen[w.ID] {
			missing = append(missing, w.ID)
		}
	}
	return missing, nil
}
