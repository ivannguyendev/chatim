package ids

import (
	"errors"
	"testing"
)

func TestNewRoomIDIsPositiveInt64AndUnique(t *testing.T) {
	seen := make(map[uint64]bool, 10_000)
	for i := 0; i < 10_000; i++ {
		id := NewRoomID()
		if id == 0 || id>>63 != 0 {
			t.Fatalf("NewRoomID() = %d, want non-zero with top bit clear", id)
		}
		if seen[id] {
			t.Fatalf("NewRoomID() repeated %d", id)
		}
		seen[id] = true
	}
}

func TestRoomIDStringRoundTrip(t *testing.T) {
	id := NewRoomID()
	got, err := ParseRoomID(FormatRoomID(id))
	if err != nil || got != id {
		t.Fatalf("ParseRoomID(FormatRoomID(%d)) = %d, %v", id, got, err)
	}
}

func TestParseRoomIDRejectsInvalid(t *testing.T) {
	for _, s := range []string{"", "0", "-1", "abc", "9223372036854775808"} {
		if _, err := ParseRoomID(s); !errors.Is(err, ErrInvalidRoomID) {
			t.Errorf("ParseRoomID(%q) err = %v, want ErrInvalidRoomID", s, err)
		}
	}
}
