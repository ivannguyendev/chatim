package app

import (
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/metrics"
)

const (
	roomSnapshotBytes    = 64 << 20
	roomPathCacheEntries = 100_000
)

type roomCaches struct {
	info  *roomcache.InfoCache
	tails *roomcache.TailCache
	snaps *roomcache.SnapshotCache
	paths *roomcache.PathCache
}

type hitCounter interface {
	Hits() uint64
	Misses() uint64
}

type watchCounter interface {
	Forgets() uint64
	Malformed() uint64
}

type roomCacheProbes struct {
	caches    map[string]hitCounter
	tailBytes func() int
	forgets   func() uint64
	malformed func() uint64
}

func wireRoomCaches(cfg config.RoomListConfig) (roomCaches, error) {
	info, err := roomcache.NewInfoCache(cfg.InfoCacheEntries)
	if err != nil {
		return roomCaches{}, fmt.Errorf("wire room info cache: %w", err)
	}
	tails, err := roomcache.NewTailCache(cfg.TailCacheBytes)
	if err != nil {
		return roomCaches{}, fmt.Errorf("wire room tail cache: %w", err)
	}
	snaps, err := roomcache.NewSnapshotCache(roomSnapshotBytes)
	if err != nil {
		return roomCaches{}, fmt.Errorf("wire room list snapshots: %w", err)
	}
	paths, err := roomcache.NewPathCache(roomPathCacheEntries)
	if err != nil {
		return roomCaches{}, fmt.Errorf("wire room list paths: %w", err)
	}
	return roomCaches{info: info, tails: tails, snaps: snaps, paths: paths}, nil
}

func wireRoomWatch(cfg config.Config, nc *nats.Conn, info *roomcache.InfoCache, log *slog.Logger) (*roomcache.Watch, error) {
	w, err := roomcache.NewWatch(nc, cfg.Stream.LiveRoot, info.Forget, log)
	if err != nil {
		return nil, fmt.Errorf("wire room watch: %w", err)
	}
	return w, nil
}

func (c roomCaches) probes(w watchCounter) roomCacheProbes {
	return roomCacheProbes{
		caches:    map[string]hitCounter{"info": c.info, "tail": c.tails, "snapshot": c.snaps, "path": c.paths},
		tailBytes: c.tails.Bytes,
		forgets:   w.Forgets,
		malformed: w.Malformed,
	}
}

func roomCacheSources(p roomCacheProbes) []metrics.Source {
	const hitHelp = "Room list cache lookups served from this core's RAM."
	const missHelp = "Room list cache lookups that read the store."
	out := make([]metrics.Source, 0, 2*len(p.caches)+3)
	for _, name := range []string{"info", "tail", "snapshot", "path"} {
		c, labels := p.caches[name], map[string]string{"cache": name}
		if c == nil {
			continue
		}
		out = append(out,
			metrics.Source{Name: "room_cache_hits_total", Help: hitHelp, Labels: labels, Read: func() float64 { return float64(c.Hits()) }},
			metrics.Source{Name: "room_cache_misses_total", Help: missHelp, Labels: labels, Read: func() float64 { return float64(c.Misses()) }},
		)
	}
	return append(out,
		metrics.Source{Name: "room_tail_cache_bytes", Help: "Estimated bytes held by the room tail cache.", Gauge: true, Read: func() float64 { return float64(p.tailBytes()) }},
		metrics.Source{Name: "room_watch_forgets_total", Help: "Rooms whose cached info this core forgot after a room_updated or member_count_changed event.", Read: func() float64 { return float64(p.forgets()) }},
		metrics.Source{Name: "room_watch_malformed_total", Help: "Room events skipped because their subject carried no valid room id.", Read: func() float64 { return float64(p.malformed()) }},
	)
}
