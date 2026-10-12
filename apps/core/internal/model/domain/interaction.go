package domain

import "time"

type ReplyCount struct {
	N         uint32
	Version   uint64
	Unsettled bool
}

type Bookmark struct {
	Room   uint64
	Thread uint64
	Seq    uint64
	Tenant string
	User   string
	On     bool
	Ver    uint32
	At     time.Time
}

type Reply struct {
	Parent MsgKey
	Room   uint64
	Thread uint64
	Seq    uint64
	Tenant string
	From   string
	Live   bool
	Ver    uint32
	At     time.Time
}
