package exchange

import "sync/atomic"

type WriteResult uint8

const (
	WriteCreated WriteResult = iota
	WriteUpdated
	WriteConflict
	WriteError
)

var WriteResults = [...]WriteResult{WriteCreated, WriteUpdated, WriteConflict, WriteError}

var writeResultLabels = [...]string{"created", "updated", "conflict", "error"}

func (r WriteResult) String() string { return writeResultLabels[r] }

type Stats struct {
	writes      [len(WriteResults)]atomic.Uint64
	storeErrors atomic.Uint64
}

func (s *Stats) Writes(r WriteResult) uint64 { return s.writes[r].Load() }

func (s *Stats) StoreErrors() uint64 { return s.storeErrors.Load() }

func (s *Stats) countWrite(r WriteResult) { s.writes[r].Add(1) }
