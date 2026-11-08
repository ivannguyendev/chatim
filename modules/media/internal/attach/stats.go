package attach

import "sync/atomic"

type Reason int

const (
	ReasonNoMessage Reason = iota
	ReasonNoUpload
	ReasonAborted
	ReasonTenant
	ReasonExpired
	ReasonInvalid
	reasonCount
)

type Result int

const (
	ResultOK Result = iota
	ResultError
	ResultCleanupError
	resultCount
)

var (
	reasonNames = [reasonCount]string{"no_message", "no_upload", "aborted", "tenant_mismatch", "upload_expired", "invalid_event"}
	resultNames = [resultCount]string{"ok", "error", "cleanup_error"}
)

type Count struct {
	Label string
	Read  func() uint64
}

type Stats struct {
	dropped [reasonCount]atomic.Uint64
	moves   [resultCount]atomic.Uint64
}

func (s *Stats) Drop(r Reason) { s.dropped[r].Add(1) }

func (s *Stats) Dropped(r Reason) uint64 { return s.dropped[r].Load() }

func (s *Stats) Moves(r Result) uint64 { return s.moves[r].Load() }

func (s *Stats) move(r Result) { s.moves[r].Add(1) }

func (s *Stats) DroppedCounts() []Count {
	out := make([]Count, reasonCount)
	for r := range reasonCount {
		out[r] = Count{Label: reasonNames[r], Read: s.dropped[r].Load}
	}
	return out
}

func (s *Stats) MoveCounts() []Count {
	out := make([]Count, resultCount)
	for r := range resultCount {
		out[r] = Count{Label: resultNames[r], Read: s.moves[r].Load}
	}
	return out
}
