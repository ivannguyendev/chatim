package store

import "time"

const MaxActiveLimit = 1000

type Activity struct {
	Room   uint64
	Thread uint64
	Seq    uint64
	At     time.Time
}

type ActiveQuery struct {
	From, To time.Time
	Tenant   string
	After    uint64
	Limit    int
}

func HourBucket(t time.Time) int64 { return t.UTC().Unix() / 3600 }

func (q ActiveQuery) Validate() error {
	switch {
	case q.Limit < 1 || q.Limit > MaxActiveLimit:
		return invalid("active rooms limit")
	case q.From.IsZero() || q.To.Before(q.From):
		return invalid("active rooms range")
	default:
		return nil
	}
}
