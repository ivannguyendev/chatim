package dedupe

import (
	"strconv"
	"strings"
	"time"
)

const (
	keyPrefix       = "chatim:cid:"
	pendingPrefix   = "p:"
	committedPrefix = "c:"
)

type Key struct {
	Room      uint64
	User, CID string
}

func (k Key) String() string {
	return keyPrefix + strconv.FormatUint(k.Room, 10) + ":" + k.User + ":" + k.CID
}

type Record struct {
	Seq, Pts  uint64
	CreatedAt time.Time
}

type Entry struct {
	Key    Key
	Record Record
}

type Status int

const (
	Absent Status = iota
	Reserved
	Committed
	PendingHere
	PendingElsewhere
)

func (s Status) String() string {
	switch s {
	case Reserved:
		return "reserved"
	case Committed:
		return "committed"
	case PendingHere:
		return "pending-here"
	case PendingElsewhere:
		return "pending-elsewhere"
	default:
		return "absent"
	}
}

type Verdict struct {
	Status Status
	Record Record
}

func pendingValue(core string) string { return pendingPrefix + core }

func committedValue(r Record) string {
	return committedPrefix + strconv.FormatUint(r.Seq, 10) + ":" + strconv.FormatUint(r.Pts, 10) + ":" + strconv.FormatInt(r.CreatedAt.UnixMilli(), 10)
}

func parseValue(v, self string) (Verdict, bool) {
	if owner, ok := strings.CutPrefix(v, pendingPrefix); ok {
		switch {
		case !validCoreID(owner):
			return Verdict{}, false
		case owner == self:
			return Verdict{Status: PendingHere}, true
		default:
			return Verdict{Status: PendingElsewhere}, true
		}
	}
	r, ok := parseCommitted(v)
	if !ok {
		return Verdict{}, false
	}
	return Verdict{Status: Committed, Record: r}, true
}

func parseCommitted(v string) (Record, bool) {
	fields := strings.Split(v, ":")
	if len(fields) != 4 || fields[0]+":" != committedPrefix {
		return Record{}, false
	}
	seq, errSeq := strconv.ParseUint(fields[1], 10, 64)
	pts, errPts := strconv.ParseUint(fields[2], 10, 64)
	ms, errMs := strconv.ParseInt(fields[3], 10, 64)
	if errSeq != nil || errPts != nil || errMs != nil || seq == 0 || pts == 0 {
		return Record{}, false
	}
	r := Record{Seq: seq, Pts: pts, CreatedAt: time.UnixMilli(ms).UTC()}
	if committedValue(r) != v {
		return Record{}, false
	}
	return r, true
}
