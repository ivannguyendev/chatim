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
	PacketKind    = "mention"
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

func NotifiedID(room string, seq uint64, ver uint32, target string) string {
	return noticeID(room, seq, "m", ver, targetKey(target))
}

func RetractedID(room string, seq uint64, ver uint32, reason string) string {
	return noticeID(room, seq, "r", ver, reason)
}

func noticeID(room string, seq uint64, mark string, ver uint32, key string) string {
	return MessageEventID(room, seq) + "-" + mark + strconv.FormatUint(uint64(ver), 10) + "-" + key
}

func targetKey(target string) string {
	kind, id, ok := strings.Cut(target, ":")
	if !ok {
		return target
	}
	return kind[:1] + ":" + id
}

func UserTarget(id string) string { return "user:" + id }

func GroupTarget(id string) string { return "group:" + id }

func NotifiedBy(room string, seq uint64, user string, ver uint32, target string) Notice {
	return Notice{ID: NotifiedID(room, seq, ver, target), Kind: KindNotified, User: user, Room: room, Seq: seq, Detail: target}
}

func RetractedFor(room string, seq uint64, user string, ver uint32, reason string) Notice {
	return Notice{ID: RetractedID(room, seq, ver, reason), Kind: KindRetracted, User: user, Room: room, Seq: seq, Detail: reason}
}

func NoticesOf(subject string, d *chatimv1.WsPublish) []Notice {
	if d.GetKind() != PacketKind {
		return nil
	}
	base, ok := noticeOf(subject, d.GetNotification())
	if !ok {
		return nil
	}
	if flaw := packetFlaw(d); flaw != "" {
		base.Detail = flaw
	}
	out := make([]Notice, 0, len(d.GetTargets().GetUsers()))
	for _, user := range d.GetTargets().GetUsers() {
		n := base
		n.User = user
		out = append(out, n)
	}
	return out
}

func packetFlaw(d *chatimv1.WsPublish) string {
	n := d.GetNotification()
	switch {
	case n.GetUser() != "":
		return "notification inside a packet names user " + n.GetUser()
	case d.GetTenant() != n.GetTenant():
		return fmt.Sprintf("packet tenant %q differs from the notification tenant %q", d.GetTenant(), n.GetTenant())
	case d.GetPart() < 1 || d.GetPart() > d.GetParts():
		return fmt.Sprintf("packet part %d of %d", d.GetPart(), d.GetParts())
	default:
		return ""
	}
}

func noticeOf(subject string, n *chatimv1.Notification) (Notice, bool) {
	out := Notice{ID: n.GetId(), Room: n.GetRoomId(), Seq: n.GetSeq(), Subject: subject}
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

func (n Notice) Key() string { return n.ID + " for " + n.User }

func CheckNotices(room string, wants, got []Notice) ([]string, error) {
	byKey := make(map[string]Notice, len(wants))
	for _, w := range wants {
		byKey[w.Key()] = w
	}
	seen := make(map[string]bool, len(wants))
	for _, n := range got {
		if n.Room != room {
			continue
		}
		w, ok := byKey[n.Key()]
		switch {
		case !ok:
			return nil, fmt.Errorf("unexpected %s %s (%s) on %s", n.Kind, n.Key(), n.Detail, n.Subject)
		case n != w:
			return nil, fmt.Errorf("notice %s is %+v, want %+v", n.Key(), n, w)
		}
		seen[n.Key()] = true
	}
	var missing []string
	for _, w := range wants {
		if !seen[w.Key()] {
			missing = append(missing, w.Key())
		}
	}
	return missing, nil
}
