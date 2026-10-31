package directory

import "context"

func (f *Follower) DirectoryPass(ctx context.Context) { f.directoryPass(ctx, &passSchedule{}) }

func (s *Stats) Stalled() bool { return s.stalledSince.Load() != 0 }
