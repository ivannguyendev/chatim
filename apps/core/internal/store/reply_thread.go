package store

import (
	"context"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type ReplyThread struct {
	Parent       MsgKey
	Tenant       string
	LastReplyAt  time.Time
	LastReplySeq uint64
	UpdatedAt    time.Time
}

type ReplyThreadCursor struct {
	At  time.Time
	Seq uint64
}

type ReplyThreads interface {
	Touch(ctx context.Context, parent MsgKey, tenant string, replySeq uint64, at time.Time) error
	List(ctx context.Context, room uint64, before ReplyThreadCursor, limit int) ([]ReplyThread, error)
}

func ValidateReplyThreadTouch(parent MsgKey, tenant string, replySeq uint64, at time.Time) error {
	if err := parent.Validate(); err != nil {
		return err
	}
	switch {
	case parent.Thread != 0:
		return invalid("parent thread")
	case parent.Room > math.MaxInt64 || parent.Seq > math.MaxInt64:
		return invalid("parent key")
	case replySeq == 0 || replySeq > math.MaxInt64:
		return invalid("reply seq")
	case at.IsZero():
		return invalid("time")
	}
	return domain.ValidTenant(tenant)
}

func ValidateReplyThreadList(room uint64, before ReplyThreadCursor, limit int) error {
	switch {
	case room == 0 || room > math.MaxInt64:
		return invalid("room id")
	case before.Seq > math.MaxInt64 || before.At.IsZero() != (before.Seq == 0):
		return invalid("cursor")
	}
	return ValidateLimit(limit, MaxPageLimit)
}

func (t ReplyThread) Before(c ReplyThreadCursor) bool {
	if c.At.IsZero() {
		return true
	}
	return t.LastReplyAt.Before(c.At) || (t.LastReplyAt.Equal(c.At) && t.Parent.Seq < c.Seq)
}
