package pbconv_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
)

func TestReactionAndPinEventIDs(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"reaction", pbconv.ReactionEventID(42, 0, 7, "bob", 1), "42-0-7-bob-n1"},
		{"thread reaction by a dashed user", pbconv.ReactionEventID(42, 3, 9, "a-n1", 12), "42-3-9-a-n1-n12"},
		{
			"widest reaction",
			pbconv.ReactionEventID(math.MaxInt64, math.MaxUint64, math.MaxUint64, "u", math.MaxUint32),
			"9223372036854775807-18446744073709551615-18446744073709551615-u-n4294967295",
		},
		{"counts", pbconv.ReactionCountsEventID(42, 0, 7, 3), "42-0-7-reactions-v3"},
		{"pin", pbconv.PinEventID(42, 5), "42-p5"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if pbconv.ReactionsCounter != "reactions" {
		t.Errorf("ReactionsCounter = %q, want reactions", pbconv.ReactionsCounter)
	}
}

func TestEventIDsNeverCollide(t *testing.T) {
	users := []string{"alice", "reactions", "v1", "a-n1", "n1", "1", "created", "p1", "a-v1", "reactions-v1"}
	seen := map[string]string{}
	add := func(id, what string) {
		t.Helper()
		if prev, dup := seen[id]; dup {
			t.Fatalf("%s and %s share the id %q", prev, what, id)
		}
		seen[id] = what
	}
	for _, room := range []uint64{1, 42} {
		add(pbconv.RoomCreatedEventID(room), fmt.Sprintf("room %d created", room))
		for _, pv := range []uint64{1, 2} {
			add(pbconv.PinEventID(room, pv), fmt.Sprintf("pin %d/p%d", room, pv))
		}
		for _, thread := range []uint64{0, 1} {
			for _, seq := range []uint64{1, 2} {
				at := fmt.Sprintf("%d/%d/%d", room, thread, seq)
				add(pbconv.MessageEventID(room, thread, seq), "message "+at)
				for _, v := range []uint32{1, 2} {
					add(pbconv.MessageChangeEventID(room, thread, seq, v), fmt.Sprintf("change %s v%d", at, v))
					add(pbconv.ReactionCountsEventID(room, thread, seq, uint64(v)), fmt.Sprintf("counts %s v%d", at, v))
					for _, u := range users {
						add(pbconv.ReactionEventID(room, thread, seq, u, v), fmt.Sprintf("reaction %s %s n%d", at, u, v))
					}
				}
			}
		}
	}
}
