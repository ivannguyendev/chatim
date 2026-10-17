package timer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	MembersCounter = "members"
	checkSize      = 29
)

var (
	ErrBadCheck  = errors.New("malformed timer check")
	counterNames = [...]string{1: MembersCounter, 2: pbconv.ReactionsCounter, 3: pbconv.RepliesCounter}
)

type Check struct {
	Room    uint64
	Thread  uint64
	Seq     uint64
	Counter string
	Op      uint32
}

func (c Check) Validate() error {
	switch {
	case c.Room == 0:
		return fmt.Errorf("%w: count check of room 0", apperr.ErrInvalidArgument)
	case c.Counter == MembersCounter && (c.Thread != 0 || c.Seq != 0):
		return fmt.Errorf("%w: member count check with a message key", apperr.ErrInvalidArgument)
	case c.Counter == MembersCounter:
		return nil
	case !pbconv.MessageCounter(c.Counter):
		return fmt.Errorf("%w: count check of counter %q", apperr.ErrInvalidArgument, c.Counter)
	}
	if err := c.Key().Validate(); err != nil {
		return err
	}
	return domain.ValidateThread(c.Thread)
}

func (c Check) Key() store.MsgKey { return store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq} }

func (c Check) ID() string {
	if c.Counter == MembersCounter {
		return "k:" + pbconv.RoomID(c.Room) + "-" + op(c.Op)
	}
	return "q:" + pbconv.RoomID(c.Room) + "-" + strconv.FormatUint(c.Thread, 10) + "-" + strconv.FormatUint(c.Seq, 10) + "-" + c.Counter + "-" + op(c.Op)
}

func (c Check) subjectTail() string {
	if c.Counter == MembersCounter {
		return pbconv.RoomID(c.Room) + "." + op(c.Op)
	}
	return pbconv.RoomID(c.Room) + "." + strconv.FormatUint(c.Thread, 10) + "." + strconv.FormatUint(c.Seq, 10) + "." + c.Counter + "." + op(c.Op)
}

func Encode(c Check) []byte {
	b := make([]byte, checkSize)
	b[0] = counterCode(c.Counter)
	binary.BigEndian.PutUint64(b[1:], c.Room)
	binary.BigEndian.PutUint64(b[9:], c.Thread)
	binary.BigEndian.PutUint64(b[17:], c.Seq)
	binary.BigEndian.PutUint32(b[25:], c.Op)
	return b
}

func Decode(b []byte) (Check, error) {
	if len(b) != checkSize || int(b[0]) >= len(counterNames) || b[0] == 0 {
		return Check{}, fmt.Errorf("%w: %d bytes", ErrBadCheck, len(b))
	}
	c := Check{
		Counter: counterNames[b[0]],
		Room:    binary.BigEndian.Uint64(b[1:]),
		Thread:  binary.BigEndian.Uint64(b[9:]),
		Seq:     binary.BigEndian.Uint64(b[17:]),
		Op:      binary.BigEndian.Uint32(b[25:]),
	}
	if err := c.Validate(); err != nil {
		return Check{}, fmt.Errorf("%w: %w", ErrBadCheck, err)
	}
	return c, nil
}

func counterCode(name string) byte {
	for i := range byte(len(counterNames)) {
		if i > 0 && counterNames[i] == name {
			return i
		}
	}
	return 0
}

func op(v uint32) string { return strconv.FormatUint(uint64(v), 10) }
