package keys

import (
	"bytes"
	"cmp"
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

func TestMsgRoundTrip(t *testing.T) {
	cases := []struct{ room, thread, seq uint64 }{
		{1, 0, 1},
		{0x5f00aa11bb22cc33, 0, 1000},
		{math.MaxInt64, 42, math.MaxUint64},
	}
	for _, c := range cases {
		b := Msg(c.room, c.thread, c.seq)
		if len(b) != MsgLen {
			t.Fatalf("Msg%+v length = %d, want %d", c, len(b), MsgLen)
		}
		room, thread, seq, err := ParseMsg(b)
		if err != nil || room != c.room || thread != c.thread || seq != c.seq {
			t.Fatalf("ParseMsg(Msg%+v) = %d %d %d %v", c, room, thread, seq, err)
		}
	}
}

func TestMsgByteOrderMatchesNumericOrder(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 10_000 {
		a := []uint64{rng.Uint64() % 4, rng.Uint64() % 4, rng.Uint64()}
		b := []uint64{rng.Uint64() % 4, rng.Uint64() % 4, rng.Uint64()}
		got := bytes.Compare(Msg(a[0], a[1], a[2]), Msg(b[0], b[1], b[2]))
		if want := compareTuples(a, b); got != want {
			t.Fatalf("compare %v vs %v = %d, want %d", a, b, got, want)
		}
	}
}

func TestMsgRangeSelectsOneTimelineWindow(t *testing.T) {
	lo, hi := MsgRange(7, 0, 10, 20)
	inRange := func(k []byte) bool { return bytes.Compare(k, lo) >= 0 && bytes.Compare(k, hi) < 0 }
	for seq := range uint64(30) {
		if got, want := inRange(Msg(7, 0, seq)), seq >= 10 && seq < 20; got != want {
			t.Errorf("seq %d in range = %v, want %v", seq, got, want)
		}
	}
	if inRange(Msg(7, 1, 15)) {
		t.Error("thread timeline leaked into main timeline range")
	}
	if inRange(Msg(6, 0, 15)) || inRange(Msg(8, 0, 15)) {
		t.Error("another room leaked into range")
	}
}

func TestEventAndEditRoundTrip(t *testing.T) {
	room, pts, err := ParseEvent(Event(9, 77))
	if err != nil || room != 9 || pts != 77 {
		t.Fatalf("ParseEvent = %d %d %v", room, pts, err)
	}
	r, th, s, v, err := ParseEdit(Edit(9, 3, 5, 2))
	if err != nil || r != 9 || th != 3 || s != 5 || v != 2 {
		t.Fatalf("ParseEdit = %d %d %d %d %v", r, th, s, v, err)
	}
	if bytes.Compare(Edit(9, 3, 5, 1), Edit(9, 3, 5, 2)) >= 0 {
		t.Fatal("edit versions must sort ascending")
	}
}

func TestParseRejectsWrongLength(t *testing.T) {
	if _, _, _, err := ParseMsg(make([]byte, MsgLen-1)); !errors.Is(err, ErrLength) {
		t.Errorf("ParseMsg short key err = %v", err)
	}
	if _, _, err := ParseEvent(make([]byte, MsgLen)); !errors.Is(err, ErrLength) {
		t.Errorf("ParseEvent wrong key err = %v", err)
	}
	if _, _, _, _, err := ParseEdit(make([]byte, MsgLen)); !errors.Is(err, ErrLength) {
		t.Errorf("ParseEdit wrong key err = %v", err)
	}
}

func compareTuples(a, b []uint64) int {
	for i := range a {
		if c := cmp.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}
