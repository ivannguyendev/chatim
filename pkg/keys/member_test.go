package keys

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestMemberKeyIsTheRoomThenTheUser(t *testing.T) {
	b := Member(0x0102030405060708, "alice")
	want := append([]byte{1, 2, 3, 4, 5, 6, 7, 8}, "alice"...)
	if !bytes.Equal(b, want) {
		t.Fatalf("Member = %x, want %x", b, want)
	}
	for _, c := range []struct {
		room uint64
		user string
	}{{1, "a"}, {9, "alice"}, {math.MaxUint64, strings.Repeat("Z", MaxMemberUser)}} {
		room, user, err := ParseMember(Member(c.room, c.user))
		if err != nil || room != c.room || user != c.user {
			t.Fatalf("ParseMember(Member(%d, %q)) = %d %q %v", c.room, c.user, room, user, err)
		}
	}
}

func TestParseMemberRejectsWrongLengths(t *testing.T) {
	if MaxMemberUser != 64 {
		t.Fatalf("MaxMemberUser = %d, want 64", MaxMemberUser)
	}
	for _, n := range []int{0, 7, 8, 8 + MaxMemberUser + 1} {
		if _, _, err := ParseMember(make([]byte, n)); !errors.Is(err, ErrLength) {
			t.Errorf("ParseMember(%d bytes) err = %v, want ErrLength", n, err)
		}
	}
}
