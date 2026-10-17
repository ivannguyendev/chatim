package mongostore

import (
	"fmt"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	boundedRoomA  uint64 = 4_000_001
	boundedRoomB  uint64 = 4_000_002
	boundedRoomC  uint64 = 4_000_003
	boundedMain          = 150
	boundedThread        = 20
	boundedLong          = "long-user-name"
)

var boundedRooms = []uint64{boundedRoomA, boundedRoomB, boundedRoomC}

func seedBoundedLookups(t *testing.T, s *Store) {
	t.Helper()
	for _, room := range boundedRooms {
		var msgs []domain.Message
		for seq := uint64(1); seq <= boundedMain; seq++ {
			msgs = append(msgs, boundedMessage(room, 0, seq))
		}
		for seq := uint64(1); seq <= boundedThread; seq++ {
			msgs = append(msgs, boundedMessage(room, 7, seq))
		}
		for i, res := range s.Insert(t.Context(), msgs) {
			if res.Outcome != store.Inserted {
				t.Fatalf("Insert %d/%d = %+v", room, i, res)
			}
		}
		members := []domain.Member{{Room: room, Tenant: "acme", User: "x", Role: domain.RoleOwner, JoinedAt: codecTime}, {Room: room, Tenant: "acme", User: boundedLong, Role: domain.RoleMember, JoinedAt: codecTime}}
		for i := range 60 {
			members = append(members, domain.Member{Room: room, Tenant: "acme", User: fmt.Sprintf("m%02d", i), Role: domain.RoleMember, JoinedAt: codecTime})
		}
		r := domain.Room{ID: room, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "x", CreatedAt: codecTime, MemberCount: len(members)}
		if err := s.Create(t.Context(), r, members); err != nil {
			t.Fatalf("Create(%d): %v", room, err)
		}
	}
	for seq := uint64(1); seq <= 40; seq++ {
		for _, user := range []string{"bob", "carolyn"} {
			x := domain.Reaction{Room: boundedRoomA, Seq: seq, Tenant: "acme", User: user, Emoji: "👍", At: codecTime}
			if _, _, err := s.Interactions().SetReaction(t.Context(), x); err != nil {
				t.Fatalf("SetReaction: %v", err)
			}
		}
	}
}

func boundedMessage(room, thread, seq uint64) domain.Message {
	return domain.Message{Room: room, Thread: thread, Seq: seq, Tenant: "acme", From: "x", CID: fmt.Sprintf("c-%d-%d-%d", room, thread, seq), Text: "hi", CreatedAt: codecTime}
}

func TestMessageLookupsAcrossRoomsAndThreadsStayBounded(t *testing.T) {
	s, db, capture := monitoredStore(t)
	seedBoundedLookups(t, s)
	var tail []store.MsgKey
	for _, room := range boundedRooms {
		for seq := uint64(boundedMain); seq > boundedMain-10; seq-- {
			tail = append(tail, store.MsgKey{Room: room, Seq: seq})
		}
	}
	capture.reset()
	if got, err := s.TailPreviews(t.Context(), tail); err != nil || len(got) != len(tail) {
		t.Fatalf("TailPreviews = %d messages, %v; want %d", len(got), err, len(tail))
	}
	expectBounded(t, "TailPreviews", explainLast(t, db, capture, messagesCollection), len(tail), len(tail))

	times := []store.MsgKey{{Room: boundedRoomA, Seq: 1}, {Room: boundedRoomB, Seq: boundedMain}, {Room: boundedRoomC, Thread: 7, Seq: 3}, {Room: boundedRoomA, Seq: 999}}
	capture.reset()
	if got, err := s.SentTimes(t.Context(), times); err != nil || len(got) != 3 {
		t.Fatalf("SentTimes = %v, %v; want 3 times", got, err)
	}
	expectBounded(t, "SentTimes", explainLast(t, db, capture, messagesCollection), len(times), 3)

	find := []store.MsgKey{{Room: boundedRoomB, Seq: boundedMain}, {Room: boundedRoomB, Seq: 1}, {Room: boundedRoomB, Thread: 7, Seq: 10}}
	capture.reset()
	got, err := s.Find(t.Context(), boundedRoomB, find)
	if err != nil || len(got) != 3 || got[0].Seq != 1 || got[1].Seq != boundedMain || got[2].Thread != 7 {
		t.Fatalf("Find = %v, %v; want seqs 1, %d and thread 7 seq 10 in key order", got, err, boundedMain)
	}
	expectBounded(t, "Find", explainLast(t, db, capture, messagesCollection), len(find), 3)
}

func TestMemberAndInteractionLookupsWithMixedKeyLengthsStayBounded(t *testing.T) {
	s, db, capture := monitoredStore(t)
	seedBoundedLookups(t, s)
	byKeys := []store.MemberKey{{Room: boundedRoomA, User: "x"}, {Room: boundedRoomB, User: boundedLong}, {Room: boundedRoomC, User: "m05"}, {Room: boundedRoomC, User: "nobody"}}
	capture.reset()
	if got, err := s.MembersByKeys(t.Context(), byKeys); err != nil || len(got) != 3 || got[0].User != "x" || got[1].User != boundedLong || got[2].User != "m05" {
		t.Fatalf("MembersByKeys = %v, %v; want x, %s, m05 in input order", got, err, boundedLong)
	}
	expectBounded(t, "MembersByKeys", explainLast(t, db, capture, membersCollection), len(byKeys), 3)

	users := []string{boundedLong, "m30", "x"}
	capture.reset()
	if got, err := s.MembersOf(t.Context(), boundedRoomA, users); err != nil || len(got) != 3 || got[0].User != boundedLong {
		t.Fatalf("MembersOf = %v, %v; want the three users in input order", got, err)
	}
	expectBounded(t, "MembersOf", explainLast(t, db, capture, membersCollection), len(users), 3)

	mine := []store.MsgKey{{Room: boundedRoomA, Seq: 3}, {Room: boundedRoomA, Seq: 30}}
	capture.reset()
	got, err := s.Interactions().MyInteractions(t.Context(), "bob", mine)
	if err != nil || len(got) != 2 || got[mine[0]].Reaction != "👍" || got[mine[1]].Bookmarked {
		t.Fatalf("MyInteractions = %v, %v; want a 👍 on both and no bookmark", got, err)
	}
	expectBounded(t, "MyInteractions", explainLast(t, db, capture, interactionsCollection), 2*len(mine), 2)
}
