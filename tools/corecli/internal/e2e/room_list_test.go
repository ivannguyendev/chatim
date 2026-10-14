package e2e_test

import (
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func TestRoomListEventIDsAndSubjectsFollowTheCore(t *testing.T) {
	cases := map[string]string{
		e2e.RoomInfoEventID("77", 2):                            "77-info-v2",
		e2e.PreferencesEventID("77", "mia", 3):                  "77-pf-mia-v3",
		e2e.LiveSubject("live", "t", "77", e2e.KindRoomUpdated): "live.t.room.77.evt.room_updated",
		e2e.LiveSubject("live", "t", "77", e2e.KindPreferences): "live.t.member.77.evt.room_preferences_changed",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestEventOfCarriesRoomUpdatesPreferencesAndSystemMessages(t *testing.T) {
	updated := &chatimv1.Event{Id: "77-info-v2", RoomId: "77", Payload: &chatimv1.Event_RoomUpdated{RoomUpdated: &chatimv1.RoomUpdated{
		Room: &chatimv1.Room{Id: "77", Name: "Kho", InfoVer: 2},
	}}}
	prefs := &chatimv1.Event{Id: "77-pf-mia-v1", RoomId: "77", Payload: &chatimv1.Event_RoomPreferencesChanged{RoomPreferencesChanged: &chatimv1.RoomPreferencesChanged{
		User: "mia", State: &chatimv1.MyRoomState{PinnedAt: timestamppb.Now(), PreferenceVer: 1},
	}}}
	system := &chatimv1.Event{Id: "77-0-3", RoomId: "77", Seq: 3, Payload: &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{
		Message: &chatimv1.Message{Seq: 3, Cid: "sys-1", Kind: chatimv1.MessageKind_MESSAGE_KIND_SYSTEM, SkipUnread: true, Meta: []byte(`{"e":1}`)},
	}}}
	cases := []struct {
		ev   *chatimv1.Event
		want e2e.Event
	}{
		{updated, e2e.Event{Kind: e2e.KindRoomUpdated, Room: "77", ID: "77-info-v2", Text: e2e.RoomInfoPayload("Kho", "", 2), Subject: "s"}},
		{prefs, e2e.Event{Kind: e2e.KindPreferences, Room: "77", ID: "77-pf-mia-v1", User: "mia", Text: e2e.PreferencesPayload(true, false, false, 1), Subject: "s"}},
		{system, e2e.Event{Kind: e2e.KindCreated, Room: "77", ID: "77-0-3", Seq: 3, CID: "sys-1", Text: `kind=system skip_unread meta={"e":1}`, Subject: "s"}},
	}
	for _, c := range cases {
		got, ok := e2e.EventOf("s", c.ev)
		if !ok || got != c.want {
			t.Fatalf("EventOf(%s) = %+v, %v; want %+v", c.ev.GetId(), got, ok, c.want)
		}
	}
	if got, _ := e2e.EventOf("s", prefs); !got.IsMember() {
		t.Fatalf("preferences event %+v is not a member event", got)
	}
	if got, _ := e2e.EventOf("s", updated); got.IsMember() {
		t.Fatalf("room_updated event %+v is a member event", got)
	}
}

func TestCreatedPayloadAddsTheMessageExtrasAfterItsLinks(t *testing.T) {
	m := &chatimv1.Message{ForwardFrom: &chatimv1.ForwardRef{RoomId: "88", Seq: 2, Author: "bob"}, SkipUnread: true, Meta: []byte("card")}
	if got, want := e2e.CreatedPayload(m), "forward_from=88-0-2 author=bob skip_unread meta=card"; got != want {
		t.Fatalf("CreatedPayload = %q, want %q", got, want)
	}
	if got := e2e.CreatedPayload(&chatimv1.Message{Kind: chatimv1.MessageKind_MESSAGE_KIND_TEXT, Text: "hi"}); got != "" {
		t.Fatalf("CreatedPayload of a plain text message = %q, want empty", got)
	}
}

func roomItem(id string, unread uint32, seq uint64, sender string, kind chatimv1.MessageKind) *chatimv1.RoomItem {
	return &chatimv1.RoomItem{
		Room: &chatimv1.Room{Id: id}, Unread: &chatimv1.Unread{Count: unread},
		Preview: &chatimv1.LastPreview{Seq: seq, Sender: sender, Kind: kind},
	}
}

func TestRoomLinesFindRoomsAndCompareUnreadAndPreview(t *testing.T) {
	items := []*chatimv1.RoomItem{
		roomItem("2", 1, 2, "bot", chatimv1.MessageKind_MESSAGE_KIND_SYSTEM),
		roomItem("1", 3, 3, "leo", chatimv1.MessageKind_MESSAGE_KIND_TEXT),
		{Room: &chatimv1.Room{Id: "4"}, Unread: &chatimv1.Unread{}},
	}
	first := e2e.RoomLine{Room: "2", Unread: 1, Preview: 2, Sender: "bot", Kind: "system"}
	if err := e2e.CheckFirst(items, first); err != nil {
		t.Fatalf("CheckFirst: %v", err)
	}
	if err := e2e.CheckLine(items, e2e.RoomLine{Room: "1", Unread: 3, Preview: 3, Sender: "leo", Kind: "text"}); err != nil {
		t.Fatalf("CheckLine: %v", err)
	}
	if err := e2e.CheckLine(items, e2e.RoomLine{Room: "4"}); err != nil {
		t.Fatalf("CheckLine of a room with no message: %v", err)
	}
	bad := []e2e.RoomLine{{Room: "1", Unread: 2, Preview: 3, Sender: "leo", Kind: "text"}, {Room: "9"}}
	for _, want := range bad {
		if err := e2e.CheckLine(items, want); err == nil {
			t.Fatalf("CheckLine(%+v) passed", want)
		}
	}
	if err := e2e.CheckFirst(items, e2e.RoomLine{Room: "1", Unread: 3, Preview: 3, Sender: "leo", Kind: "text"}); err == nil {
		t.Fatal("CheckFirst passed the second room")
	}
	if err := e2e.CheckFirst(nil, first); err == nil {
		t.Fatal("CheckFirst passed an empty list")
	}
}

func TestCheckOrderWantsTheExactRoomsInOrder(t *testing.T) {
	items := []*chatimv1.RoomItem{{Room: &chatimv1.Room{Id: "3"}}, {Room: &chatimv1.Room{Id: "1"}}}
	if err := e2e.CheckOrder("main", items, []string{"3", "1"}); err != nil {
		t.Fatalf("CheckOrder: %v", err)
	}
	for _, want := range [][]string{{"1", "3"}, {"3"}, {"3", "1", "1"}} {
		if err := e2e.CheckOrder("main", items, want); err == nil {
			t.Fatalf("CheckOrder(%v) passed for rooms 3,1", want)
		}
	}
}
