package exchange

import "sync/atomic"

type Result uint8

const (
	ResultOK Result = iota
	ResultInvalid
	ResultDisabled
	ResultError
)

var Results = [...]Result{ResultOK, ResultInvalid, ResultDisabled, ResultError}

var resultLabels = [...]string{"ok", "invalid", "disabled", "error"}

func (r Result) String() string { return resultLabels[r] }

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
	exchanges   [len(Results)]atomic.Uint64
	writes      [len(WriteResults)]atomic.Uint64
	storeErrors atomic.Uint64
}

func (s *Stats) Exchanges(r Result) uint64 { return s.exchanges[r].Load() }

func (s *Stats) Writes(r WriteResult) uint64 { return s.writes[r].Load() }

func (s *Stats) StoreErrors() uint64 { return s.storeErrors.Load() }

func (s *Stats) count(r Result) { s.exchanges[r].Add(1) }

func (s *Stats) countWrite(r WriteResult) { s.writes[r].Add(1) }
