package keys

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestReactionKeyIsTheMessageKeyPlusTheUser(t *testing.T) {
	b := Reaction(9, 3, 5, "alice")
	if len(b) != MsgLen+5 || !bytes.HasPrefix(b, Msg(9, 3, 5)) || string(b[MsgLen:]) != "alice" {
		t.Fatalf("Reaction = %x, want keys.Msg(9, 3, 5) followed by the user bytes", b)
	}
	for _, user := range []string{"a", "alice", strings.Repeat("Z", MaxReactionUser)} {
		room, thread, seq, got, err := ParseReaction(Reaction(9, 3, 5, user))
		if err != nil || room != 9 || thread != 3 || seq != 5 || got != user {
			t.Fatalf("ParseReaction(Reaction(9, 3, 5, %q)) = %d %d %d %q %v", user, room, thread, seq, got, err)
		}
	}
}

func TestReactionKeysOfAMessageStayInsideItsRange(t *testing.T) {
	lo, hi := Msg(9, 0, 5), Msg(9, 0, 6)
	for _, user := range []string{"-", "a", "alice", "zz", strings.Repeat("Z", MaxReactionUser)} {
		k := Reaction(9, 0, 5, user)
		if bytes.Compare(k, lo) <= 0 || bytes.Compare(k, hi) >= 0 {
			t.Fatalf("Reaction(9, 0, 5, %q) = %x is outside (%x, %x)", user, k, lo, hi)
		}
	}
}

func TestPinKeysRoundTripAndSortByVersion(t *testing.T) {
	b := Pin(9, 77)
	room, pv, err := ParsePin(b)
	if len(b) != PinLen || PinLen != 16 || err != nil || room != 9 || pv != 77 {
		t.Fatalf("ParsePin(Pin(9, 77)) = %d %d %v from %d bytes", room, pv, err, len(b))
	}
	if bytes.Compare(Pin(9, 1), Pin(9, 2)) >= 0 || bytes.Compare(Pin(9, math.MaxUint64), Pin(10, 0)) >= 0 {
		t.Fatal("pin keys must sort by room then version")
	}
}

func TestParseReactionAndPinRejectWrongLengths(t *testing.T) {
	for _, n := range []int{0, MsgLen, MsgLen + MaxReactionUser + 1} {
		if _, _, _, _, err := ParseReaction(make([]byte, n)); !errors.Is(err, ErrLength) {
			t.Errorf("ParseReaction(%d bytes) err = %v, want ErrLength", n, err)
		}
	}
	for _, n := range []int{0, PinLen - 1, PinLen + 1} {
		if _, _, err := ParsePin(make([]byte, n)); !errors.Is(err, ErrLength) {
			t.Errorf("ParsePin(%d bytes) err = %v, want ErrLength", n, err)
		}
	}
}
