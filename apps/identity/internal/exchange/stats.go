package exchange

import "sync/atomic"

type WriteResult uint8

const (
	WriteUpdated WriteResult = iota
	WriteConflict
	WriteError
)

var WriteResults = [...]WriteResult{WriteUpdated, WriteConflict, WriteError}

var writeResultLabels = [...]string{"updated", "conflict", "error"}

func (r WriteResult) String() string { return writeResultLabels[r] }

type LinkOutcome uint8

const (
	LinkCreated LinkOutcome = iota
	LinkExisting
	LinkHealed
	LinkPending
	LinkError
)

var LinkOutcomes = [...]LinkOutcome{LinkCreated, LinkExisting, LinkHealed, LinkPending, LinkError}

var linkOutcomeLabels = [...]string{"created", "existing", "healed", "pending", "error"}

func (o LinkOutcome) String() string { return linkOutcomeLabels[o] }

type Stats struct {
	writes      [len(WriteResults)]atomic.Uint64
	links       [len(LinkOutcomes)]atomic.Uint64
	storeErrors atomic.Uint64
}

func (s *Stats) Writes(r WriteResult) uint64 { return s.writes[r].Load() }

func (s *Stats) Links(o LinkOutcome) uint64 { return s.links[o].Load() }

func (s *Stats) StoreErrors() uint64 { return s.storeErrors.Load() }

func (s *Stats) countWrite(r WriteResult) { s.writes[r].Add(1) }

func (s *Stats) countLink(o LinkOutcome) { s.links[o].Add(1) }
