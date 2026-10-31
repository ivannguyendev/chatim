package directory

import "context"

func (f *Follower) DirectoryPass(ctx context.Context) { f.directoryPass(ctx) }

func (s *Stats) Stalled() bool { return s.stalledSince.Load() != 0 }
