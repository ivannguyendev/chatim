package storetest

import (
	"cmp"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type memberFeedCase struct {
	name string
	run  func(t *testing.T, s MemberRooms, hidden store.Hidden, cur store.Cursor)
}

func RunMemberFeed(t *testing.T, open func(t *testing.T) (MemberRooms, store.Hidden, store.ChangeFeed)) {
	t.Helper()
	for _, c := range []memberFeedCase{
		{"create and add members feed every doc whose ver changed", memberFeedJoins},
		{"an owner change feeds its writes in order and counts feed nothing", memberFeedOwners},
		{"read moves feed the new read ver and no-ops feed nothing", memberFeedReads},
		{"a raised clear and a new hide feed one change each", memberFeedClearAndHide},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, hidden, feed := open(t)
			c.run(t, s, hidden, openCursor(t, feed))
		})
	}
}

func assertMemberChange(t *testing.T, c store.Change, kind store.ChangeKind, user string, ver uint64) {
	t.Helper()
	got := uint64(c.Member.Ver)
	if kind == store.ReadChanged {
		got = c.Member.ReadVer
	}
	if c.Kind != kind || c.Member.Room != roomA || c.Member.User != user || got != ver || c.Msg.Seq != 0 || c.Hidden.User != "" {
		t.Fatalf("change = %+v, want kind %d for %q at ver %d", c, kind, user, ver)
	}
}

func mustMarkRead(t *testing.T, s MemberRooms, user string, seq uint64) {
	t.Helper()
	if _, changed, err := s.MarkRead(t.Context(), roomA, user, seq); err != nil || !changed {
		t.Fatalf("MarkRead(%q, %d) = %v, %v; want a move", user, seq, changed, err)
	}
}

func memberFeedJoins(t *testing.T, s MemberRooms, _ store.Hidden, cur store.Cursor) {
	_, bob := crew(t, s)
	removeMember(t, s, bob, secs(2))
	j := domain.Join{Room: roomA, Tenant: tenant, RequestID: "req-add", By: "alice", At: secs(10), ReadSeq: 4}
	for range 2 {
		if _, err := s.AddMembers(t.Context(), j, []string{"carol", "bob", "alice"}); err != nil {
			t.Fatalf("AddMembers: %v", err)
		}
	}
	mustMarkRead(t, s, "alice", 5)
	got := nextChanges(t, cur, 7)
	if got[0].Kind != store.RoomInserted || got[0].Room.ID != roomA {
		t.Fatalf("first change = %+v, want the room insert", got[0])
	}
	assertMemberChange(t, got[1], store.MemberChanged, "alice", 1)
	assertMemberChange(t, got[2], store.MemberChanged, "bob", 1)
	assertMemberChange(t, got[3], store.MemberChanged, "bob", 2)
	joined := slices.SortedFunc(slices.Values(got[4:6]), func(a, b store.Change) int { return cmp.Compare(a.Member.User, b.Member.User) })
	assertMemberChange(t, joined[0], store.MemberChanged, "bob", 3)
	assertMemberChange(t, joined[1], store.MemberChanged, "carol", 1)
	assertMemberChange(t, got[6], store.ReadChanged, "alice", 1)
}

func memberFeedOwners(t *testing.T, s MemberRooms, _ store.Hidden, cur store.Cursor) {
	alice, bob := crew(t, s)
	nextChanges(t, cur, 3)
	up, out := step(bob, domain.RoleOwner, domain.MemberActive), step(alice, domain.RoleOwner, domain.MemberRemoved)
	if _, err := s.ChangeOwners(t.Context(), roomA, []string{"alice", "bob"}, plan(up, out)); err != nil {
		t.Fatalf("ChangeOwners: %v", err)
	}
	c, err := s.AddMemberCount(t.Context(), roomA, 1)
	if err != nil {
		t.Fatalf("AddMemberCount: %v", err)
	}
	if _, ok, err := s.SetMemberCount(t.Context(), roomA, c.Ver, 7); err != nil || !ok {
		t.Fatalf("SetMemberCount = %v, %v", ok, err)
	}
	mustMarkRead(t, s, "bob", 3)
	got := nextChanges(t, cur, 3)
	assertMemberChange(t, got[0], store.MemberChanged, "bob", 2)
	assertMemberChange(t, got[1], store.MemberChanged, "alice", 2)
	assertMemberChange(t, got[2], store.ReadChanged, "bob", 1)
}

func memberFeedReads(t *testing.T, s MemberRooms, _ store.Hidden, cur store.Cursor) {
	_, bob := crew(t, s)
	nextChanges(t, cur, 3)
	steps := []struct {
		unread bool
		seq    uint64
	}{{false, 5}, {false, 3}, {true, 2}, {true, 4}}
	for _, st := range steps {
		mark(t, s, st.unread, st.seq)
	}
	changeMember(t, s, bob, domain.RoleAdmin, domain.MemberActive, 0, secs(9))
	got := nextChanges(t, cur, 3)
	assertMemberChange(t, got[0], store.ReadChanged, "bob", 1)
	assertMemberChange(t, got[1], store.ReadChanged, "bob", 2)
	assertMemberChange(t, got[2], store.MemberChanged, "bob", 2)
}

func memberFeedClearAndHide(t *testing.T, s MemberRooms, hidden store.Hidden, cur store.Cursor) {
	crew(t, s)
	nextChanges(t, cur, 3)
	for _, sec := range []int{5, 3, 5} {
		if _, _, err := s.ClearHistory(t.Context(), roomA, "bob", secs(sec)); err != nil {
			t.Fatalf("ClearHistory(%d): %v", sec, err)
		}
	}
	hide := hiddenMark("bob", roomA, sideThread, 5, 6)
	mustHide(t, hidden, hide, hide)
	mustMarkRead(t, s, "bob", 2)
	got := nextChanges(t, cur, 3)
	if c := got[0]; c.Kind != store.HistoryCleared || c.Member.Room != roomA || c.Member.User != "bob" || c.Hidden.User != "" {
		t.Fatalf("clear change = %+v, want history cleared for bob", c)
	}
	h := got[1].Hidden
	if got[1].Kind != store.MessageHidden || h.User != "bob" || h.Room != roomA || h.Thread != sideThread || h.Seq != 5 || got[1].Member.User != "" {
		t.Fatalf("hide change = %+v, want bob hiding %d/%d/5", got[1], roomA, sideThread)
	}
	assertMemberChange(t, got[2], store.ReadChanged, "bob", 1)
}
