package work

import (
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

const (
	RecordSize    = 37
	maxUserLen    = 64
	MaxRecordSize = RecordSize + 1 + maxUserLen
)

var (
	ErrBadRecord   = fmt.Errorf("%w: work record", apperr.ErrInvalidArgument)
	ErrUnknownKind = fmt.Errorf("%w: unknown kind", ErrBadRecord)

	epoch = time.Unix(0, 0)
)

type Record struct {
	Kind        store.ChangeKind
	Room        uint64
	Thread      uint64
	Seq         uint64
	Version     uint32
	User        string
	CommittedAt time.Time
}

func KnownKind(k store.ChangeKind) bool {
	switch k {
	case store.MessageInserted, store.RoomInserted, store.EditInserted, store.ReactionChanged, store.PinInserted:
		return true
	default:
		return false
	}
}

func RecordOf(c store.Change) Record {
	r := Record{Kind: c.Kind, CommittedAt: c.CommittedAt}
	switch c.Kind {
	case store.MessageInserted:
		r.Room, r.Thread, r.Seq = c.Msg.Room, c.Msg.Thread, c.Msg.Seq
	case store.RoomInserted, store.MemberCountCheck:
		r.Room = c.Room.ID
	case store.EditInserted:
		r.Room, r.Thread, r.Seq, r.Version = c.Edit.Room, c.Edit.Thread, c.Edit.Seq, c.Edit.Version
	case store.ReactionChanged:
		x := c.Reaction
		r.Room, r.Thread, r.Seq, r.Version, r.User = x.Room, x.Thread, x.Seq, x.N, x.User
	case store.PinInserted:
		r.Room, r.Seq = c.Pin.Room, c.Pin.PV
	case store.MemberChanged:
		r.Room, r.Version, r.User = c.Member.Room, c.Member.Ver, c.Member.User
	case store.ReadChanged:
		r.Room, r.Version, r.User = c.Member.Room, narrowVersion(c.Member.ReadVer), c.Member.User
	case store.MessageHidden:
		h := c.Hidden
		r.Room, r.Thread, r.Seq, r.User = h.Room, h.Thread, h.Seq, h.User
	case store.HistoryCleared:
		r.Room, r.User = c.Member.Room, c.Member.User
	}
	return r
}

func narrowVersion(v uint64) uint32 {
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

func (r Record) ID() string {
	switch r.Kind {
	case store.MessageInserted:
		return "m:" + pbconv.MessageEventID(r.Room, r.Thread, r.Seq)
	case store.RoomInserted:
		return "r:" + pbconv.RoomID(r.Room)
	case store.EditInserted:
		return "e:" + pbconv.MessageChangeEventID(r.Room, r.Thread, r.Seq, r.Version)
	case store.ReactionChanged:
		return "x:" + pbconv.ReactionEventID(r.Room, r.Thread, r.Seq, r.User, r.Version)
	case store.PinInserted:
		return "p:" + pbconv.PinEventID(r.Room, r.Seq)
	default:
		return ""
	}
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
