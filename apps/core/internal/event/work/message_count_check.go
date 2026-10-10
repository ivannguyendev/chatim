package work

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func MessageCountCheck(key store.MsgKey, counter string, op uint32, at time.Time) (Record, error) {
	if err := key.Validate(); err != nil {
		return Record{}, err
	}
	if err := domain.ValidateThread(key.Thread); err != nil {
		return Record{}, err
	}
	if !pbconv.MessageCounter(counter) {
		return Record{}, fmt.Errorf("%w: message count check of counter %q", apperr.ErrInvalidArgument, counter)
	}
	return Record{Kind: store.MessageCountCheck, Room: key.Room, Thread: key.Thread, Seq: key.Seq, Version: op, User: counter, CommittedAt: at}, nil
}

func RandomOp() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])
}
