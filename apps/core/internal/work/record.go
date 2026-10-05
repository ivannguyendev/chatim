package work

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const RecordSize = 33

var (
	ErrBadRecord = fmt.Errorf("%w: work record", apperr.ErrInvalidArgument)

	epoch = time.Unix(0, 0)
)

type Record struct {
	Kind        store.ChangeKind
	Room        uint64
	Thread      uint64
	Seq         uint64
	CommittedAt time.Time
}

func RecordOf(c store.Change) Record {
	r := Record{Kind: c.Kind, CommittedAt: c.CommittedAt}
	switch c.Kind {
	case store.MessageInserted:
		r.Room, r.Thread, r.Seq = c.Msg.Room, c.Msg.Thread, c.Msg.Seq
	case store.RoomInserted:
		r.Room = c.Room.ID
	}
	return r
}

func (r Record) ID() string {
	switch r.Kind {
	case store.MessageInserted:
		return "m:" + pbconv.MessageEventID(r.Room, r.Thread, r.Seq)
	case store.RoomInserted:
		return "r:" + pbconv.RoomID(r.Room)
	default:
		return ""
	}
}

func Encode(r Record) []byte {
	b := make([]byte, 1, RecordSize)
	b[0] = byte(r.Kind)
	b = binary.BigEndian.AppendUint64(b, r.Room)
	b = binary.BigEndian.AppendUint64(b, r.Thread)
	b = binary.BigEndian.AppendUint64(b, r.Seq)
	return binary.BigEndian.AppendUint64(b, unixNano(r.CommittedAt))
}

func Decode(b []byte) (Record, error) {
	if len(b) != RecordSize {
		return Record{}, fmt.Errorf("%w: %d bytes, want %d", ErrBadRecord, len(b), RecordSize)
	}
	kind := store.ChangeKind(b[0])
	if kind != store.MessageInserted && kind != store.RoomInserted {
		return Record{}, fmt.Errorf("%w: kind %d", ErrBadRecord, kind)
	}
	ns := binary.BigEndian.Uint64(b[25:])
	if ns > math.MaxInt64 {
		return Record{}, fmt.Errorf("%w: commit time out of range", ErrBadRecord)
	}
	return Record{
		Kind:        kind,
		Room:        binary.BigEndian.Uint64(b[1:]),
		Thread:      binary.BigEndian.Uint64(b[9:]),
		Seq:         binary.BigEndian.Uint64(b[17:]),
		CommittedAt: time.Unix(0, int64(ns)).UTC(),
	}, nil
}

func Partition(room uint64, partitions int) int {
	return int(slotmap.Of(room)) % partitions
}

func Subject(root string, partition int) string {
	return root + ".p" + strconv.Itoa(partition)
}

func Message(root string, partitions int, r Record) *nats.Msg {
	m := &nats.Msg{Subject: Subject(root, Partition(r.Room, partitions)), Data: Encode(r), Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, r.ID())
	return m
}

func unixNano(t time.Time) uint64 {
	if t.Before(epoch) {
		return 0
	}
	ns := t.UnixNano()
	if ns < 0 {
		return 0
	}
	return uint64(ns)
}
