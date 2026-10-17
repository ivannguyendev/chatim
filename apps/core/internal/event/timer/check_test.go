package timer_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
)

func TestChecksSurviveTheirEncoding(t *testing.T) {
	for _, c := range []timer.Check{
		{Room: room, Counter: timer.MembersCounter, Op: 42},
		{Room: room, Seq: 9, Counter: pbconv.ReactionsCounter, Op: 1},
		{Room: room, Seq: 1<<63 + 5, Counter: pbconv.RepliesCounter, Op: 1<<32 - 1},
	} {
		got, err := timer.Decode(timer.Encode(c))
		if err != nil || got != c {
			t.Errorf("Decode(Encode(%+v)) = %+v, %v", c, got, err)
		}
	}
}

func TestCheckIDsNameTheTargetAndTheOp(t *testing.T) {
	cases := map[string]timer.Check{
		"k:7340000001-42":             {Room: room, Counter: timer.MembersCounter, Op: 42},
		"q:7340000001-0-9-replies-17": {Room: room, Seq: 9, Counter: pbconv.RepliesCounter, Op: 17},
	}
	for want, c := range cases {
		if got := c.ID(); got != want {
			t.Errorf("ID(%+v) = %q, want %q", c, got, want)
		}
	}
}

func TestDecodeRefusesMalformedChecks(t *testing.T) {
	valid := timer.Encode(timer.Check{Room: room, Seq: 3, Counter: pbconv.ReactionsCounter, Op: 1})
	mutate := func(f func(b []byte)) []byte {
		b := append([]byte(nil), valid...)
		f(b)
		return b
	}
	cases := map[string][]byte{
		"empty":              nil,
		"short":              valid[:28],
		"long":               append(append([]byte(nil), valid...), 0),
		"no counter":         mutate(func(b []byte) { b[0] = 0 }),
		"unknown counter":    mutate(func(b []byte) { b[0] = 4 }),
		"room 0":             mutate(func(b []byte) { binary.BigEndian.PutUint64(b[1:], 0) }),
		"thread reply count": mutate(func(b []byte) { binary.BigEndian.PutUint64(b[9:], 5) }),
		"seq 0":              mutate(func(b []byte) { binary.BigEndian.PutUint64(b[17:], 0) }),
		"members with a seq": mutate(func(b []byte) { b[0] = 1 }),
	}
	for name, b := range cases {
		if c, err := timer.Decode(b); !errors.Is(err, timer.ErrBadCheck) {
			t.Errorf("%s: Decode = %+v, %v; want ErrBadCheck", name, c, err)
		}
	}
}
