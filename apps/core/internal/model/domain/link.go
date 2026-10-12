package domain

import (
	"math"
	"time"
)

type MsgKey struct{ Room, Thread, Seq uint64 }

type ReplyRef struct {
	Thread uint64
	Seq    uint64
}

type ForwardRef struct {
	Room   uint64
	Thread uint64
	Seq    uint64
	Author string
	SentAt time.Time
}

func ValidateReply(r *ReplyRef) error {
	if r == nil {
		return nil
	}
	if r.Thread != 0 || r.Seq == 0 || r.Seq == math.MaxUint64 {
		return invalid("reply_to")
	}
	return nil
}
