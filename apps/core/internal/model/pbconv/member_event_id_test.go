package pbconv_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
)

func TestMemberCountAndReadEventIDs(t *testing.T) {
	clearedAt := time.UnixMilli(1_759_700_000_123)
	cases := []struct{ name, got, want string }{
		{"member", pbconv.MemberEventID(42, "bob", 3), "42-mb-bob-v3"},
		{"widest member", pbconv.MemberEventID(math.MaxInt64, "u", math.MaxUint32), "9223372036854775807-mb-u-v4294967295"},
		{"member count", pbconv.MemberCountEventID(42, 7), "42-members-v7"},
		{"read", pbconv.ReadEventID(42, "bob", 5), "42-rd-bob-v5"},
		{"widest read", pbconv.ReadEventID(42, "u", math.MaxUint64), "42-rd-u-v18446744073709551615"},
		{"hidden", pbconv.HiddenEventID(42, "bob", 3, 9), "42-hd-bob-3-9"},
		{"cleared", pbconv.ClearedEventID(42, "bob", clearedAt), "42-cl-bob-1759700000123"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestMemberEventIDsNeverCollideWithOtherKinds(t *testing.T) {
	users := []string{"mb", "v1", "a-v1", "members", "rd", "mb-bob-v1", "bob", "hd", "cl", "1", "created", "p1"}
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
		for _, v := range []uint64{1, 2} {
			add(pbconv.PinEventID(room, v), fmt.Sprintf("pin %d/p%d", room, v))
			add(pbconv.MemberCountEventID(room, v), fmt.Sprintf("member count %d v%d", room, v))
			add(pbconv.MessageEventID(room, 0, v), fmt.Sprintf("message %d/0/%d", room, v))
			add(pbconv.MessageChangeEventID(room, 0, 1, uint32(v)), fmt.Sprintf("change %d/0/1 v%d", room, v))
			add(pbconv.ReactionCountsEventID(room, 0, 1, v), fmt.Sprintf("counts %d/0/1 v%d", room, v))
			for _, u := range users {
				add(pbconv.MemberEventID(room, u, uint32(v)), fmt.Sprintf("member %d %s v%d", room, u, v))
				add(pbconv.ReadEventID(room, u, v), fmt.Sprintf("read %d %s v%d", room, u, v))
				add(pbconv.HiddenEventID(room, u, 0, v), fmt.Sprintf("hidden %d %s 0/%d", room, u, v))
				add(pbconv.ClearedEventID(room, u, time.UnixMilli(int64(v))), fmt.Sprintf("cleared %d %s %d", room, u, v))
				add(pbconv.ReactionEventID(room, 0, 1, u, uint32(v)), fmt.Sprintf("reaction %d %s n%d", room, u, v))
			}
		}
	}
}
