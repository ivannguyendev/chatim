package store

import "strconv"

type MsgKey struct{ Room, Thread, Seq uint64 }

type Outcome uint8

const (
	Inserted Outcome = iota + 1
	Duplicate
	Unknown
	Rejected
)

func (o Outcome) String() string {
	switch o {
	case Inserted:
		return "inserted"
	case Duplicate:
		return "duplicate"
	case Unknown:
		return "unknown"
	case Rejected:
		return "rejected"
	default:
		return "outcome(" + strconv.Itoa(int(o)) + ")"
	}
}

type Result struct {
	Outcome Outcome
	Err     error
}

type Anchor uint8

const (
	Latest Anchor = iota + 1
	Oldest
	Before
	After
)

type PageQuery struct {
	Room, Thread uint64
	Anchor       Anchor
	Seq          uint64
	Limit        int
}
