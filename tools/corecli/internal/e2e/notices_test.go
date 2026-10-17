package e2e_test

import (
	"strings"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const noticeRoom = "77"

func notified(user string, seq uint64, ver uint32, target *chatimv1.MentionTarget) *chatimv1.Notification {
	return &chatimv1.Notification{
		Id: e2e.NotifiedID(noticeRoom, seq, user, ver), Tenant: "e2e", User: user, RoomId: noticeRoom, Seq: seq, MessageVer: ver,
		Payload: &chatimv1.Notification_MentionNotified{MentionNotified: &chatimv1.MentionNotified{
			Message: &chatimv1.Message{RoomId: noticeRoom, Seq: seq, Ver: ver}, Target: target,
		}},
	}
}

func retracted(user string, seq uint64, ver uint32, reason chatimv1.RetractReason) *chatimv1.Notification {
	return &chatimv1.Notification{
		Id: e2e.RetractedID(noticeRoom, seq, user, ver), Tenant: "e2e", User: user, RoomId: noticeRoom, Seq: seq, MessageVer: ver,
		Payload: &chatimv1.Notification_MentionRetracted{MentionRetracted: &chatimv1.MentionRetracted{Reason: reason}},
	}
}

func noticeOf(t *testing.T, n *chatimv1.Notification) e2e.Notice {
	t.Helper()
	got, ok := e2e.NoticeOf(e2e.NotifySubject("notify", "e2e", n.GetUser()), n)
	if !ok {
		t.Fatalf("NoticeOf(%v) = not a notice", n)
	}
	return got
}

func TestNoticeIDsFollowTheCatalog(t *testing.T) {
	if got := e2e.NotifiedID("77", 5, "minh", 1); got != "77-0-5-minh-m1" {
		t.Fatalf("NotifiedID = %s", got)
	}
	if got := e2e.RetractedID("77", 5, "minh", 2); got != "77-0-5-minh-r2" {
		t.Fatalf("RetractedID = %s", got)
	}
	if got := e2e.NotifySubject("notify", "e2e", "minh"); got != "notify.e2e.minh.mention" {
		t.Fatalf("NotifySubject = %s", got)
	}
}

func TestNoticeOfDescribesTheTargetOrTheReason(t *testing.T) {
	group := &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "team-sale"}
	all := &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_ALL}
	cases := map[string]struct {
		n    *chatimv1.Notification
		want e2e.Notice
	}{
		"group": {notified("an", 3, 0, group), e2e.NotifiedBy(noticeRoom, 3, "an", 0, e2e.GroupTarget("team-sale"))},
		"all":   {notified("an", 4, 0, all), e2e.NotifiedBy(noticeRoom, 4, "an", 0, e2e.AllTarget)},
		"edit":  {retracted("minh", 5, 2, chatimv1.RetractReason_RETRACT_REASON_EDITED), e2e.RetractedFor(noticeRoom, 5, "minh", 2, e2e.ReasonEdited)},
		"del":   {retracted("an", 3, 1, chatimv1.RetractReason_RETRACT_REASON_DELETED), e2e.RetractedFor(noticeRoom, 3, "an", 1, e2e.ReasonDeleted)},
	}
	for name, c := range cases {
		want := c.want
		want.Subject = e2e.NotifySubject("notify", "e2e", want.User)
		if got := noticeOf(t, c.n); got != want {
			t.Fatalf("%s: NoticeOf = %+v, want %+v", name, got, want)
		}
	}
	if _, ok := e2e.NoticeOf("x", &chatimv1.Notification{Id: "1"}); ok {
		t.Fatal("NoticeOf of a notification without a payload = a notice")
	}
}

func TestNoticeOfFlagsASnapshotOfAnotherMessage(t *testing.T) {
	n := notified("an", 3, 0, &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "an"})
	n.GetMentionNotified().GetMessage().Seq = 9
	if got := noticeOf(t, n); !strings.Contains(got.Detail, "snapshot") {
		t.Fatalf("detail = %q, want the snapshot mismatch named", got.Detail)
	}
}

func TestCheckNoticesReportsMissingUnexpectedAndWrongNotices(t *testing.T) {
	want := e2e.NotifiedBy(noticeRoom, 3, "an", 0, e2e.GroupTarget("team-sale"))
	want.Subject = e2e.NotifySubject("notify", "e2e", "an")
	other := noticeOf(t, notified("chi", 3, 0, &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "team-sale"}))
	elsewhere := other
	elsewhere.Room = "78"
	missing, err := e2e.CheckNotices(noticeRoom, []e2e.Notice{want}, []e2e.Notice{elsewhere})
	if err != nil || len(missing) != 1 || missing[0] != want.ID {
		t.Fatalf("CheckNotices = %v, %v; want %s missing and notices of other rooms ignored", missing, err, want.ID)
	}
	if missing, err := e2e.CheckNotices(noticeRoom, []e2e.Notice{want}, []e2e.Notice{want, want}); err != nil || len(missing) != 0 {
		t.Fatalf("CheckNotices with a redelivery = %v, %v; want all seen", missing, err)
	}
	if _, err := e2e.CheckNotices(noticeRoom, []e2e.Notice{want}, []e2e.Notice{other}); err == nil || !strings.Contains(err.Error(), other.ID) {
		t.Fatalf("CheckNotices with an unwanted notice = %v, want an error naming %s", err, other.ID)
	}
	wrong := want
	wrong.Detail = e2e.AllTarget
	if _, err := e2e.CheckNotices(noticeRoom, []e2e.Notice{want}, []e2e.Notice{wrong}); err == nil {
		t.Fatal("CheckNotices with a wrong target = nil, want an error")
	}
}
