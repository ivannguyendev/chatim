package wsload

import (
	"slices"
	"testing"
	"time"
)

func TestLayoutPutsEveryUserInOneGroupAndSpreadsNodes(t *testing.T) {
	l := Layout{Users: 10, Groups: 4, Nodes: 2}
	seen := map[int]int{}
	for g := range l.Groups {
		members := l.Members(g)
		if len(members) != l.MembersOf(g) {
			t.Fatalf("group %d: %d members, MembersOf %d", g, len(members), l.MembersOf(g))
		}
		for _, u := range members {
			if l.GroupOf(u) != g {
				t.Fatalf("user %d listed in group %d, GroupOf %d", u, g, l.GroupOf(u))
			}
			seen[u]++
		}
		if !slices.Contains(members, l.SenderOf(g)) {
			t.Fatalf("sender %d of group %d is not a member of %v", l.SenderOf(g), g, members)
		}
	}
	if len(seen) != l.Users {
		t.Fatalf("users covered = %d, want %d", len(seen), l.Users)
	}
	nodes := map[int]int{}
	for u := range l.Users {
		nodes[l.NodeOf(u)]++
	}
	if nodes[0] != 6 || nodes[1] != 4 {
		t.Fatalf("users per node = %v, want 6 and 4", nodes)
	}
	if got := l.Members(1); !slices.Equal(got, []int{1, 5, 9}) {
		t.Fatalf("Members(1) = %v", got)
	}
}

func TestLayoutSendersSpreadOverNodes(t *testing.T) {
	l := Layout{Users: 10_000, Groups: 500, Nodes: 2}
	nodes := map[int]int{}
	for g := range l.Groups {
		nodes[l.NodeOf(l.SenderOf(g))]++
	}
	if nodes[0] != 250 || nodes[1] != 250 {
		t.Fatalf("senders per node = %v, want 250 each", nodes)
	}
}

func TestLayoutValidate(t *testing.T) {
	cases := []struct {
		name string
		l    Layout
		ok   bool
	}{
		{"default", Layout{Users: 10_000, Groups: 500, Nodes: 2}, true},
		{"no group", Layout{Users: 10, Groups: 0, Nodes: 1}, false},
		{"more groups than users", Layout{Users: 3, Groups: 4, Nodes: 1}, false},
		{"groups too large", Layout{Users: 1001, Groups: 2, Nodes: 1}, false},
		{"no node", Layout{Users: 10, Groups: 2, Nodes: 0}, false},
	}
	for _, c := range cases {
		if err := c.l.Validate(500); (err == nil) != c.ok {
			t.Errorf("%s: Validate = %v, want ok %v", c.name, err, c.ok)
		}
	}
}

func TestStampRoundTrip(t *testing.T) {
	sent := time.Unix(1_800_000_000, 123_456_789)
	text := StampText("wl0a1b", sent)
	got, ok := ParseStamp("wl0a1b", text)
	if !ok || !got.Equal(sent) {
		t.Fatalf("ParseStamp(%q) = %v, %v, want %v", text, got, ok, sent)
	}
	for _, bad := range []string{"other 123", "wl0a1b", "wl0a1b x", "wl0a1b -5", "wl0a1b2 123"} {
		if _, ok := ParseStamp("wl0a1b", bad); ok {
			t.Errorf("ParseStamp(%q) accepted", bad)
		}
	}
}

func TestSeqSetReportsEachSeqOnce(t *testing.T) {
	var s SeqSet
	for _, seq := range []uint64{1, 2, 64, 200, 0} {
		if !s.Add(seq) {
			t.Fatalf("Add(%d) first time = false", seq)
		}
	}
	for _, seq := range []uint64{1, 2, 64, 200, 0} {
		if s.Add(seq) {
			t.Fatalf("Add(%d) second time = true", seq)
		}
	}
	if !s.Add(63) || !s.Add(65) {
		t.Fatal("neighbours of a stored seq must be new")
	}
}
