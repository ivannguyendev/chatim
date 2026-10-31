package app

import (
	"context"
	"errors"
	"math"
	"runtime/debug"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/config"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/iamsync"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	"github.com/ivannguyendev/chatim/pkg/revocation"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

type directoryParts struct {
	cache     *directory.Cache
	list      *revocation.List
	stats     *directory.Stats
	loader    *directory.Loader
	follower  *directory.Follower
	gate      *userlimit.Gate
	users     *userlimit.Users
	apiStats  *api.DirectoryStats
	feedStats *api.FeedStats
}

func newDirectory(cfg config.Config, st Store, deps Deps) (*directoryParts, error) {
	list, err := iamsync.NewList(tokenLeeway)
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
	uri := cfg.DatabaseURI()
	listen := func(ctx context.Context) (store.Listener, error) { return deps.OpenListener(ctx, uri) }
	return &directoryParts{
		cache: cache, list: list, stats: stats, gate: gate, users: users,
		apiStats: &api.DirectoryStats{}, feedStats: &api.FeedStats{},
		loader:   &directory.Loader{Reader: st, Revocations: st, Cache: cache, List: list, Slots: cfg.DirectoryLoadSlots, Stats: stats},
		follower: &directory.Follower{Listen: listen, Reader: st, Revocations: st, Cache: cache, List: list, Stats: stats},
	}, nil
}

func (d *directoryParts) run(ctx context.Context, onLoaded func()) error {
	d.loader.OnLoaded = onLoaded
	err := d.loader.Run(ctx)
	if err == nil {
		err = d.follower.Run(ctx)
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func openListener(ctx context.Context, uri string) (store.Listener, error) {
	return pgstore.OpenListener(ctx, uri)
}

func currentMemoryLimit() int64 { return debug.SetMemoryLimit(-1) }

func memoryLimitSet(limit int64) bool { return limit != math.MaxInt64 }

func directoryMetricSources(d *directoryParts, memoryLimited bool, now func() time.Time) []metrics.Source {
	s, feed, calls := d.stats, d.feedStats, d.apiStats
	out := []metrics.Source{
		gauge("directory_ready", "1 once the directory is loaded into memory.", func() float64 { return boolValue(s.Ready()) }),
		gauge("directory_entries", "Users held in the in-memory directory.", func() float64 { return float64(s.Entries()) }),
		gauge("directory_load_seconds", "Duration of the last full directory load.", s.LastLoadSeconds),
		counter("directory_load_failures_total", "Failed full directory loads.", s.LoadFailures),
		gauge("directory_lag_seconds", "Seconds since the follower last caught up with the change feed; 0 before the first load.", func() float64 {
			caught := s.LastCaughtUp()
			if caught.IsZero() {
				return 0
			}
			return max(now().Sub(caught).Seconds(), 0)
		}),
		gauge("directory_listen_up", "1 while the Postgres LISTEN connection is open.", func() float64 { return boolValue(s.ListenUp()) }),
		gauge("directory_horizon_stalled_seconds", "Seconds the change feed has waited on an open transaction below the horizon.", func() float64 { return s.HorizonStalledSeconds(now()) }),
		counter("directory_read_errors_total", "Failed change feed reads by the follower.", s.ReadErrors),
		counter("directory_revocation_errors_total", "Failed revocation list reads by the follower.", s.RevocationErrors),
		gauge("directory_feed_waiting", "Directory feed long polls waiting now.", func() float64 { return float64(feed.Waiting.Load()) }),
		counter("get_users_ids_total", "User ids looked up by GetUsers.", calls.GetUsersIDs.Load),
		counter("user_limited_total", "Directory calls refused by the per-user rate or in-flight limit.", calls.UserLimited.Load),
		counter("inflight_rejected_total", "Directory calls refused because the node in-flight cap was full.", calls.InflightRejected.Load),
		gauge("memory_limit_set", "1 when GOMEMLIMIT is set.", func() float64 { return boolValue(memoryLimited) }),
	}
	for label, c := range map[string]func() uint64{
		"ok": feed.OK.Load, "empty": feed.Empty.Load, "bad_request": feed.BadRequest.Load, "unauthorized": feed.Unauthorized.Load, "error": feed.Errors.Load,
	} {
		out = append(out, labelledCounter("directory_feed_requests_total", "Directory feed requests on the internal port by result.", "result", label, c))
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
