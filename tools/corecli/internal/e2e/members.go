package e2e

import (
	"strconv"
	"strings"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	KindRoomCreated     = "room_created"
	KindMemberAdded     = "member_added"
	KindMemberRemoved   = "member_removed"
	KindRoleChanged     = "member_role_changed"
	KindPriorityChanged = "member_priority_changed"
	KindMemberCount     = "member_count_changed"
	KindRead            = "read_updated"
	KindHidden          = "message_hidden"
	KindCleared         = "history_cleared"
)

func RoomCreatedEventID(room string) string { return room + "-created" }

func MemberEventID(room, user string, ver uint32) string {
	return room + "-mb-" + user + "-v" + strconv.FormatUint(uint64(ver), 10)
}

func MemberCountEventID(room string, ver uint64) string {
	return room + "-members-v" + strconv.FormatUint(ver, 10)
}

func ReadEventID(room, user string, readVer uint64) string {
	return room + "-rd-" + user + "-v" + strconv.FormatUint(readVer, 10)
}

func HiddenEventID(room, user string, thread, seq uint64) string {
	return room + "-hd-" + user + "-" + strconv.FormatUint(thread, 10) + "-" + strconv.FormatUint(seq, 10)
}

func ClearedEventID(room, user string, at time.Time) string {
	return room + "-cl-" + user + "-" + strconv.FormatInt(at.UnixMilli(), 10)
}

func LiveSubject(root, tenant, room, kind string) string {
	return root + "." + tenant + "." + classOf(kind) + "." + room + ".evt." + kind
}

func classOf(kind string) string {
	switch kind {
	case KindRoomCreated, KindPinned, KindUnpinned, KindMemberCount:
		return "room"
	case KindMemberAdded, KindMemberRemoved, KindRoleChanged, KindPriorityChanged, KindRead, KindHidden, KindCleared, KindBookmark:
		return "member"
	default:
		return "message"
	}
}

func (e Event) IsMember() bool { return e.Kind == KindMemberCount || classOf(e.Kind) == "member" }

func RoleName(r chatimv1.MemberRole) string {
	return strings.ToLower(strings.TrimPrefix(r.String(), "MEMBER_ROLE_"))
}

func AddedPayload(role string, readSeq, readVer uint64) string {
	return "role=" + role + " " + ReadPayload(readSeq, readVer)
}

func RemovedPayload(reason, previousRole string) string {
	return "reason=" + reason + " previous_role=" + previousRole
}

func RolePayload(role, previous string) string { return "role=" + role + " previous_role=" + previous }

func PriorityPayload(priority, previous int32) string {
	return "priority=" + strconv.Itoa(int(priority)) + " previous_priority=" + strconv.Itoa(int(previous))
}

func CountPayload(n int32) string { return "member_count=" + strconv.Itoa(int(n)) }

func ReadPayload(seq, ver uint64) string {
	return "read_seq=" + strconv.FormatUint(seq, 10) + " read_ver=" + strconv.FormatUint(ver, 10)
}

func HiddenPayload(thread, seq uint64) string {
	return "thread=" + strconv.FormatUint(thread, 10) + " seq=" + strconv.FormatUint(seq, 10)
}

func ClearedPayload(at time.Time) string {
	return "cleared_at=" + strconv.FormatInt(at.UnixMilli(), 10)
}

func memberOf(subject string, ev *chatimv1.Event) (Event, bool) {
	out := Event{Room: ev.GetRoomId(), ID: ev.GetId(), Subject: subject}
	switch {
	case ev.GetRoomCreated() != nil:
		out.Kind = KindRoomCreated
	case ev.GetMemberAdded() != nil:
		p := ev.GetMemberAdded()
		out.Kind, out.User, out.Version = KindMemberAdded, p.GetUser(), p.GetVer()
		out.Text = AddedPayload(RoleName(p.GetRole()), p.GetReadSeq(), p.GetReadVer())
	case ev.GetMemberRemoved() != nil:
		p := ev.GetMemberRemoved()
		reason := strings.ToLower(strings.TrimPrefix(p.GetReason().String(), "MEMBER_REMOVED_REASON_"))
		out.Kind, out.User, out.Version, out.Text = KindMemberRemoved, p.GetUser(), p.GetVer(), RemovedPayload(reason, RoleName(p.GetPreviousRole()))
	case ev.GetMemberRoleChanged() != nil:
		p := ev.GetMemberRoleChanged()
		out.Kind, out.User, out.Version = KindRoleChanged, p.GetUser(), p.GetVer()
		out.Text = RolePayload(RoleName(p.GetRole()), RoleName(p.GetPreviousRole()))
	case ev.GetMemberPriorityChanged() != nil:
		p := ev.GetMemberPriorityChanged()
		out.Kind, out.User, out.Version = KindPriorityChanged, p.GetUser(), p.GetVer()
		out.Text = PriorityPayload(p.GetPriority(), p.GetPreviousPriority())
	case ev.GetMemberCountChanged() != nil:
		out.Kind, out.Text = KindMemberCount, CountPayload(ev.GetMemberCountChanged().GetMemberCount())
	case ev.GetReadUpdated() != nil:
		p := ev.GetReadUpdated()
		out.Kind, out.User, out.Text = KindRead, p.GetUser(), ReadPayload(p.GetReadSeq(), p.GetReadVer())
	case ev.GetMessageHidden() != nil:
		p := ev.GetMessageHidden()
		out.Kind, out.User, out.Seq, out.Text = KindHidden, p.GetUser(), p.GetSeq(), HiddenPayload(p.GetThreadRoot(), p.GetSeq())
	case ev.GetHistoryCleared() != nil:
		p := ev.GetHistoryCleared()
		out.Kind, out.User, out.Text = KindCleared, p.GetUser(), ClearedPayload(p.GetClearedAt().AsTime())
	default:
		return Event{}, false
	}
	return out, true
}
