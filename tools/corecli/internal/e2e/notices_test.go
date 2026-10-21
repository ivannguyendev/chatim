package e2e_test

import (
	"strings"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const (
	noticeRoom    = "77"
	noticeSubject = "websocket.publish.e2e"
)

func packet(n *chatimv1.Notification, users ...string) *chatimv1.WsPublish {
	return &chatimv1.WsPublish{Id: n.GetId() + "-p1-0", Tenant: "e2e", Kind: "mention", Notification: n, Targets: &chatimv1.Targets{Users: users}, Part: 1, Parts: 1}
}

func notified(seq uint64, ver uint32, target *chatimv1.MentionTarget, detail string) *chatimv1.Notification {
	return &chatimv1.Notification{
		Id: e2e.NotifiedID(noticeRoom, seq, ver, detail), Tenant: "e2e", RoomId: noticeRoom, Seq: seq, MessageVer: ver,
		Payload: &chatimv1.Notification_MentionNotified{MentionNotified: &chatimv1.MentionNotified{
			Message: &chatimv1.Message{RoomId: noticeRoom, Seq: seq, Ver: ver}, Target: target,
		}},
	}
}

func retracted(seq uint64, ver uint32, reason chatimv1.RetractReason, key string) *chatimv1.Notification {
	return &chatimv1.Notification{
		Id: e2e.RetractedID(noticeRoom, seq, ver, key), Tenant: "e2e", RoomId: noticeRoom, Seq: seq, MessageVer: ver,
		Payload: &chatimv1.Notification_MentionRetracted{MentionRetracted: &chatimv1.MentionRetracted{Reason: reason}},
	}
}

func withSubject(n e2e.Notice) e2e.Notice {
	n.Subject = noticeSubject
	return n
}

func TestNoticeIDsFollowTheCatalog(t *testing.T) {
	cases := map[string]string{
		e2e.NotifiedID("77", 5, 1, e2e.UserTarget("minh")):       "77-0-5-m1-u:minh",
		e2e.NotifiedID("77", 5, 0, e2e.GroupTarget("team-sale")): "77-0-5-m0-g:team-sale",
		e2e.NotifiedID("77", 5, 0, e2e.AllTarget):                "77-0-5-m0-all",
		e2e.RetractedID("77", 5, 2, e2e.ReasonEdited):            "77-0-5-r2-edited",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("id = %s, want %s", got, want)
		}
	}
}

func TestNoticesOfExpandsThePacketToItsUsers(t *testing.T) {
	group := &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "team-sale"}
	got := e2e.NoticesOf(noticeSubject, packet(notified(3, 0, group, e2e.GroupTarget("team-sale")), "an", "chi"))
	want := []e2e.Notice{
		withSubject(e2e.NotifiedBy(noticeRoom, 3, "an", 0, e2e.GroupTarget("team-sale"))),
		withSubject(e2e.NotifiedBy(noticeRoom, 3, "chi", 0, e2e.GroupTarget("team-sale"))),
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("NoticesOf = %+v, want %+v", got, want)
	}
}

func TestNoticesOfDescribesTheTargetOrTheReason(t *testing.T) {
	all := &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_ALL}
	cases := map[string]struct {
		n    *chatimv1.Notification
		want e2e.Notice
	}{
		"all":  {notified(4, 0, all, e2e.AllTarget), e2e.NotifiedBy(noticeRoom, 4, "an", 0, e2e.AllTarget)},
		"edit": {retracted(5, 2, chatimv1.RetractReason_RETRACT_REASON_EDITED, e2e.ReasonEdited), e2e.RetractedFor(noticeRoom, 5, "an", 2, e2e.ReasonEdited)},
		"del":  {retracted(3, 1, chatimv1.RetractReason_RETRACT_REASON_DELETED, e2e.ReasonDeleted), e2e.RetractedFor(noticeRoom, 3, "an", 1, e2e.ReasonDeleted)},
	}
	for name, c := range cases {
		if got := e2e.NoticesOf(noticeSubject, packet(c.n, "an")); len(got) != 1 || got[0] != withSubject(c.want) {
			t.Fatalf("%s: NoticesOf = %+v, want %+v", name, got, withSubject(c.want))
		}
	}
	if got := e2e.NoticesOf("x", packet(&chatimv1.Notification{Id: "1"}, "an")); len(got) != 0 {
		t.Fatal("NoticesOf of a notification without a payload gave notices")
	}
	other := packet(notified(4, 0, all, e2e.AllTarget), "an")
	other.Kind = "call"
	if got := e2e.NoticesOf(noticeSubject, other); len(got) != 0 {
		t.Fatal("NoticesOf of another kind gave notices")
	}
}

func TestNoticesOfFlagsAFlawedPacket(t *testing.T) {
	user := &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "an"}
	snapshot := notified(3, 0, user, e2e.UserTarget("an"))
	snapshot.GetMentionNotified().GetMessage().Seq = 9
	named := notified(3, 0, user, e2e.UserTarget("an"))
	named.User = "an"
	tenant := packet(notified(3, 0, user, e2e.UserTarget("an")), "an")
	tenant.Tenant = "other"
	part := packet(notified(3, 0, user, e2e.UserTarget("an")), "an")
	part.Part = 2
	cases := map[string]*chatimv1.WsPublish{"snapshot": packet(snapshot, "an"), "names user": packet(named, "an"), "tenant": tenant, "part": part}
	for name, d := range cases {
		got := e2e.NoticesOf(noticeSubject, d)
		if len(got) != 1 || got[0].Detail == e2e.UserTarget("an") {
			t.Errorf("%s: NoticesOf = %+v, want the flaw named in the detail", name, got)
		}
	}
}

func TestCheckNoticesReportsMissingUnexpectedAndWrongNotices(t *testing.T) {
	want := withSubject(e2e.NotifiedBy(noticeRoom, 3, "an", 0, e2e.GroupTarget("team-sale")))
	other := withSubject(e2e.NotifiedBy(noticeRoom, 3, "chi", 0, e2e.GroupTarget("team-sale")))
	elsewhere := other
	elsewhere.Room = "78"
	missing, err := e2e.CheckNotices(noticeRoom, []e2e.Notice{want}, []e2e.Notice{elsewhere})
	if err != nil || len(missing) != 1 || missing[0] != want.Key() {
		t.Fatalf("CheckNotices = %v, %v; want %s missing and notices of other rooms ignored", missing, err, want.Key())
	}
	if missing, err := e2e.CheckNotices(noticeRoom, []e2e.Notice{want}, []e2e.Notice{want, want}); err != nil || len(missing) != 0 {
		t.Fatalf("CheckNotices with a redelivery = %v, %v; want all seen", missing, err)
	}
	if _, err := e2e.CheckNotices(noticeRoom, []e2e.Notice{want}, []e2e.Notice{other}); err == nil || !strings.Contains(err.Error(), other.Key()) {
		t.Fatalf("CheckNotices with an unwanted notice = %v, want an error naming %s", err, other.Key())
	}
	wrong := want
	wrong.Detail = e2e.AllTarget
	if _, err := e2e.CheckNotices(noticeRoom, []e2e.Notice{want}, []e2e.Notice{wrong}); err == nil {
		t.Fatal("CheckNotices with a wrong target = nil, want an error")
	}
}
