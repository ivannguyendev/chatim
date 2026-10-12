package keys

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

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

func TestParsePinRejectsWrongLengths(t *testing.T) {
	for _, n := range []int{0, PinLen - 1, PinLen + 1} {
		if _, _, err := ParsePin(make([]byte, n)); !errors.Is(err, ErrLength) {
			t.Errorf("ParsePin(%d bytes) err = %v, want ErrLength", n, err)
		}
	}
}
