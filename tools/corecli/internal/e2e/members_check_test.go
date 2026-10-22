package e2e_test

import (
	"slices"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const dm = "77"

func wantOf(r, kind, id, payload string) e2e.Want {
	return e2e.Want{ID: id, Kind: kind, Subject: e2e.LiveSubject("live", "e2e", r, kind), Payload: payload}
}

func seenAs(w e2e.Want, r, text string) e2e.Event {
	return e2e.Event{Kind: w.Kind, Room: r, ID: w.ID, Subject: w.Subject, Text: text}
}

func TestCheckLiveFindsEachWantedEventByID(t *testing.T) {
	added := wantOf(room, e2e.KindMemberAdded, e2e.MemberEventID(room, bob, 1), e2e.AddedPayload("member", 80, 1))
	count := wantOf(room, e2e.KindMemberCount, e2e.MemberCountEventID(room, 2), e2e.CountPayload(3))
	created := wantOf(dm, e2e.KindRoomCreated, e2e.RoomCreatedEventID(dm), "")
	msg := wantOf(dm, e2e.KindCreated, e2e.MessageEventID(dm, 1), "")
	wants := []e2e.Want{created, msg, added, count}
	rooms := []string{room, dm}
	other := e2e.Event{Kind: e2e.KindMemberAdded, Room: "9", ID: "9-mb-x-v1", Subject: "live.e2e.member.9.evt.member_added"}
	earlier := e2e.Event{Kind: e2e.KindCreated, Room: room, ID: e2e.MessageEventID(room, 1), Subject: "live.e2e.message.42.evt.msg_created"}
	pin := e2e.Event{Kind: e2e.KindPinned, Room: room, ID: e2e.PinEventID(room, 1)}
	events := []e2e.Event{other, earlier, pin, seenAs(added, room, added.Payload), seenAs(created, dm, "anything"), seenAs(added, room, added.Payload)}
	missing, err := e2e.CheckLive(rooms, wants, events)
	if err != nil || !slices.Equal(missing, []string{msg.ID, count.ID}) {
		t.Fatalf("CheckLive = %v, %v; want %s and %s missing in want order", missing, err, msg.ID, count.ID)
	}
	all := append(slices.Clone(events), seenAs(msg, dm, ""), seenAs(count, room, count.Payload))
	if missing, err := e2e.CheckLive(rooms, wants, all); err != nil || len(missing) != 0 {
		t.Fatalf("CheckLive(all) = %v, %v; want none missing", missing, err)
	}
	wrongSubject := seenAs(count, room, count.Payload)
	wrongSubject.Subject = "live.e2e.member.42.evt.member_count_changed"
	wrongKind := seenAs(added, room, added.Payload)
	wrongKind.Kind = e2e.KindRoleChanged
	bad := map[string]e2e.Event{
		"subject":                        wrongSubject,
		"want member_added":              wrongKind,
		`"member_count=2"`:               seenAs(count, room, e2e.CountPayload(2)),
		"unexpected member_role_changed": {Kind: e2e.KindRoleChanged, Room: room, ID: e2e.MemberEventID(room, bob, 2)},
	}
	for part, ev := range bad {
		_, err := e2e.CheckLive(rooms, wants, []e2e.Event{ev})
		expectErr(t, err, part)
	}
}

func TestCheckLiveMatchesReadPositionsByEachUsersFinalPosition(t *testing.T) {
	final := wantOf(room, e2e.KindRead, e2e.ReadWantID(room, bob, 3), e2e.ReadPayload(80, 3))
	subject := final.Subject
	read := func(text string) e2e.Event {
		return e2e.Event{Kind: e2e.KindRead, Room: room, ID: room + "-rp-x", Subject: subject, Text: text}
	}
	cases := map[string]struct {
		events []e2e.Event
		seen   bool
	}{
		"final position among others":      {[]e2e.Event{read("e2e-amy read_seq=2 read_ver=1;" + bob + " read_seq=80 read_ver=3")}, true},
		"intermediate then final":          {[]e2e.Event{read(bob + " read_seq=79 read_ver=2"), read(bob + " read_seq=80 read_ver=3")}, true},
		"only an intermediate version":     {[]e2e.Event{read(bob + " read_seq=79 read_ver=2")}, false},
		"same ver at another seq":          {[]e2e.Event{read(bob + " read_seq=81 read_ver=3")}, false},
		"another user at the same version": {[]e2e.Event{read("e2e-amy read_seq=80 read_ver=3")}, false},
		"other room":                       {[]e2e.Event{{Kind: e2e.KindRead, Room: "9", Subject: subject, Text: bob + " read_seq=80 read_ver=3"}}, false},
		"wrong subject":                    {[]e2e.Event{{Kind: e2e.KindRead, Room: room, Subject: "live.e2e.message.42.evt.read_positions", Text: bob + " read_seq=80 read_ver=3"}}, false},
		"malformed entries":                {[]e2e.Event{read("garbage;" + bob + " read_seq=x")}, false},
	}
	for name, c := range cases {
		missing, err := e2e.CheckLive([]string{room}, []e2e.Want{final}, c.events)
		if err != nil {
			t.Fatalf("%s: CheckLive = %v, want no error for read positions", name, err)
		}
		if got := len(missing) == 0; got != c.seen {
			t.Fatalf("%s: seen %v (missing %v), want %v", name, got, missing, c.seen)
		}
	}
}

func TestCheckMemberReplyAndCleared(t *testing.T) {
	first := e2e.MemberReply{Changed: true, Ver: 2, Detail: "previous_role=member"}
	if err := e2e.CheckMemberReply("set-role", first, first); err != nil {
		t.Fatalf("CheckMemberReply(same) = %v", err)
	}
	noop := e2e.MemberReply{Ver: 2}
	if err := e2e.CheckMemberReply("set-role again", noop, e2e.MemberReply{Ver: 2, Detail: "previous_role=admin"}); err != nil {
		t.Fatalf("CheckMemberReply(no-op compares changed and ver only) = %v", err)
	}
	expectErr(t, e2e.CheckMemberReply("set-role", first, e2e.MemberReply{Changed: true, Ver: 3, Detail: first.Detail}), "ver 3")
	expectErr(t, e2e.CheckMemberReply("set-role", first, e2e.MemberReply{Ver: 2, Detail: first.Detail}), "changed false")
	expectErr(t, e2e.CheckMemberReply("set-role", first, e2e.MemberReply{Changed: true, Ver: 2, Detail: "previous_role=admin"}), "previous_role=admin")
	if err := e2e.CheckReadReply("read", 80, 1, 80, 1); err != nil {
		t.Fatalf("CheckReadReply(same) = %v", err)
	}
	expectErr(t, e2e.CheckReadReply("read", 80, 1, 79, 2), "read_seq 79 read_ver 2")
	added := []*chatimv1.AddedMember{{User: "e2e-carol", Ver: 1}, {User: bob, Ver: 1}}
	if got := e2e.FormatAdded(added); got != "e2e-bob:1,e2e-carol:1" || e2e.FormatAdded(nil) != "" {
		t.Fatalf("FormatAdded = %q, want sorted user:ver pairs", got)
	}
	as := acks(3)
	page := messagesOf(as[1:])
	page[0].Hidden, page[0].Text = true, ""
	if err := e2e.CheckClearedPage(page, 2, as[2]); err != nil {
		t.Fatalf("CheckClearedPage(good) = %v", err)
	}
	expectErr(t, e2e.CheckClearedPage(page[:1], 2, as[2]), "1 messages")
	expectErr(t, e2e.CheckClearedPage(messagesOf(as[1:]), 2, as[2]), "seq 2")
	shownHidden := messagesOf(as[1:])
	shownHidden[0].Hidden, shownHidden[0].Text, shownHidden[1].Hidden = true, "", true
	expectErr(t, e2e.CheckClearedPage(shownHidden, 2, as[2]), "seq 3")
}
