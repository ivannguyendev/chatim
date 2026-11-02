package trust

import "sync/atomic"

type Stats struct {
	reloadErrors atomic.Uint64
}

func (s *Stats) ReloadErrors() uint64 { return s.reloadErrors.Load() }
