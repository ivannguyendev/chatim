package work

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func Encode(r Record) []byte {
	b := make([]byte, 1, RecordSize+1+len(r.User))
	b[0] = byte(r.Kind)
	b = binary.BigEndian.AppendUint64(b, r.Room)
	b = binary.BigEndian.AppendUint64(b, r.Thread)
	b = binary.BigEndian.AppendUint64(b, r.Seq)
	b = binary.BigEndian.AppendUint32(b, r.Version)
	b = binary.BigEndian.AppendUint64(b, unixNano(r.CommittedAt))
	return appendUser(b, r.User)
}

func appendUser(b []byte, user string) []byte {
	n := len(user)
	if n < 1 || n > maxUserLen {
		return b
	}
	return append(append(b, byte(n)), user...)
}

func Decode(b []byte) (Record, error) {
	if len(b) < RecordSize || len(b) > MaxRecordSize {
		return Record{}, fmt.Errorf("%w: %d bytes, want %d to %d", ErrBadRecord, len(b), RecordSize, MaxRecordSize)
	}
	user, err := userTail(b[RecordSize:])
	if err != nil {
		return Record{}, err
	}
	ns := binary.BigEndian.Uint64(b[29:])
	if ns > math.MaxInt64 {
		return Record{}, fmt.Errorf("%w: commit time out of range", ErrBadRecord)
	}
	r := Record{
		Kind:        store.ChangeKind(b[0]),
		Room:        binary.BigEndian.Uint64(b[1:]),
		Thread:      binary.BigEndian.Uint64(b[9:]),
		Seq:         binary.BigEndian.Uint64(b[17:]),
		Version:     binary.BigEndian.Uint32(b[25:]),
		User:        user,
		CommittedAt: time.Unix(0, int64(ns)).UTC(),
	}
	if err := checkKind(r); err != nil {
		return Record{}, err
	}
	return r, nil
}

func userTail(tail []byte) (string, error) {
	if len(tail) == 0 {
		return "", nil
	}
	if n := int(tail[0]); n < 1 || n > maxUserLen || len(tail) != 1+n {
		return "", fmt.Errorf("%w: user tail of %d bytes", ErrBadRecord, len(tail))
	}
	return string(tail[1:]), nil
}

func checkKind(r Record) error {
	switch {
	case r.Kind == 0:
		return fmt.Errorf("%w: kind 0", ErrBadRecord)
	case !KnownKind(r.Kind):
		return fmt.Errorf("%w %d", ErrUnknownKind, r.Kind)
	case r.Kind == store.MessageCountCheck && !pbconv.MessageCounter(r.User):
		return fmt.Errorf("%w: message count check of counter %q", ErrBadRecord, r.User)
	case carriesUser(r.Kind) && domain.ValidUser(r.User) != nil:
		return fmt.Errorf("%w: kind %d record without a valid tail", ErrBadRecord, r.Kind)
	case !carriesTail(r.Kind) && r.User != "":
		return fmt.Errorf("%w: kind %d carries a user", ErrBadRecord, r.Kind)
	default:
		return nil
	}
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
