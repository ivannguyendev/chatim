package domain

import "time"

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
