package app

import (
	"context"
	"math"
	"runtime/debug"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/api"
	"github.com/ivannguyendev/chatim/apps/identity/internal/config"
	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/pkg/auth"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
	"github.com/ivannguyendev/chatim/pkg/core/userlimit"
)

type directoryParts struct {
	cache    *directory.Cache
	list     *auth.RevocationList
	stats    *directory.Stats
	mirror   *directory.Mirror
	gate     *userlimit.Gate
	users    *userlimit.Users
	apiStats *api.DirectoryStats
}

func newDirectory(cfg config.Config, st Store) (*directoryParts, error) {
	list, err := auth.NewNodeRevocationList(tokenLeeway)
	if err != nil {
		return nil, err
	}
	gate, err := userlimit.NewGate(cfg.InflightMax)
	if err != nil {
		return nil, err
	}
	users, err := userlimit.NewUsers(userlimit.UserConfig{Rate: cfg.UserRate, Burst: cfg.UserBurst, Inflight: cfg.UserInflight, Capacity: cfg.UserLimiters})
	if err != nil {
		return nil, err
	}
	cache := directory.NewCache()
	stats := directory.NewStats(cache)
	return &directoryParts{
		cache: cache, list: list, stats: stats, gate: gate, users: users,
		apiStats: &api.DirectoryStats{},
		mirror:   &directory.Mirror{Source: st, Cache: cache, List: list, Stats: stats, Slots: cfg.DirectoryLoadSlots, Recheck: cfg.TrustReload},
	}, nil
}

func (d *directoryParts) run(ctx context.Context, onLoaded func()) error {
	d.mirror.OnLoaded = onLoaded
	return d.mirror.Run(ctx)
}

func currentMemoryLimit() int64 { return debug.SetMemoryLimit(-1) }

func memoryLimitSet(limit int64) bool { return limit != math.MaxInt64 }

func directoryMetricSources(d *directoryParts, memoryLimited bool, now func() time.Time) []metrics.Source {
	s, calls := d.stats, d.apiStats
	out := []metrics.Source{
		gauge("directory_ready", "1 once the directory is loaded into memory.", func() float64 { return boolValue(s.Ready()) }),
		gauge("directory_entries", "Users held in the in-memory directory.", func() float64 { return float64(s.Entries()) }),
		gauge("directory_load_seconds", "Duration of the last full directory load.", s.LastLoadSeconds),
		counter("directory_load_failures_total", "Failed full directory loads.", s.LoadFailures),
		gauge("directory_lag_seconds", "Seconds since the change stream last said the follower caught up; 0 before the first load.", func() float64 {
			caught := s.LastCaughtUp()
			if caught.IsZero() {
				return 0
			}
			return max(now().Sub(caught).Seconds(), 0)
		}),
		gauge("directory_stream_up", "1 while the directory change stream is open.", func() float64 { return boolValue(s.StreamUp()) }),
		counter("directory_stream_reloads_total", "Full directory reloads because the change stream lost its position.", s.StreamReloads),
		counter("directory_read_errors_total", "Failed change stream opens and reads by the follower.", s.ReadErrors),
		counter("directory_revocation_errors_total", "Failed revocation list reads by the follower.", s.RevocationErrors),
		counter("get_users_ids_total", "User ids looked up by GetUsers.", calls.GetUsersIDs.Load),
		counter("user_limited_total", "Directory calls refused by the per-user rate or in-flight limit.", calls.UserLimited.Load),
		counter("inflight_rejected_total", "Directory calls refused because the node in-flight cap was full.", calls.InflightRejected.Load),
		gauge("memory_limit_set", "1 when GOMEMLIMIT is set.", func() float64 { return boolValue(memoryLimited) }),
	}
	for label, c := range map[string]func() uint64{
		"ok": calls.GetUsersOK.Load, "bad_request": calls.GetUsersBadRequest.Load, "unauthenticated": calls.GetUsersUnauthenticated.Load,
		"limited": calls.GetUsersLimited.Load, "unavailable": calls.GetUsersUnavailable.Load, "error": calls.GetUsersErrors.Load,
	} {
		out = append(out, labelledCounter("get_users_total", "GetUsers calls by result.", "result", label, c))
	}
	return out
}

func gauge(name, help string, read func() float64) metrics.Source {
	return metrics.Source{Name: name, Help: help, Gauge: true, Read: read}
}

func counter(name, help string, read func() uint64) metrics.Source {
	return metrics.Source{Name: name, Help: help, Read: func() float64 { return float64(read()) }}
}

func labelledCounter(name, help, key, value string, read func() uint64) metrics.Source {
	src := counter(name, help, read)
	src.Labels = map[string]string{key: value}
	return src
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
