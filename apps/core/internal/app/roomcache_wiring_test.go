package app

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

type fakeHits struct{ hits, misses uint64 }

func (f fakeHits) Hits() uint64 { return f.hits }

func (f fakeHits) Misses() uint64 { return f.misses }

type zeroWatch struct{}

func (zeroWatch) Forgets() uint64 { return 0 }

func (zeroWatch) Malformed() uint64 { return 0 }

func fakeRoomCacheProbes() roomCacheProbes {
	return roomCacheProbes{
		caches: map[string]hitCounter{
			"info": fakeHits{1, 2}, "tail": fakeHits{3, 4}, "snapshot": fakeHits{5, 6}, "path": fakeHits{7, 8},
		},
		tailBytes: func() int { return 9 },
		forgets:   func() uint64 { return 10 },
		malformed: func() uint64 { return 11 },
	}
}

func TestRoomCacheMetricsReadEachCacheByLabel(t *testing.T) {
	got := map[string]float64{}
	gauges := map[string]bool{}
	for _, s := range roomCacheSources(fakeRoomCacheProbes()) {
		key := s.Name
		if c := s.Labels["cache"]; c != "" {
			key += "{" + c + "}"
		}
		got[key] = s.Read()
		gauges[s.Name] = s.Gauge
	}
	want := map[string]float64{
		"room_cache_hits_total{info}": 1, "room_cache_misses_total{info}": 2,
		"room_cache_hits_total{tail}": 3, "room_cache_misses_total{tail}": 4,
		"room_cache_hits_total{snapshot}": 5, "room_cache_misses_total{snapshot}": 6,
		"room_cache_hits_total{path}": 7, "room_cache_misses_total{path}": 8,
		"room_tail_cache_bytes": 9, "room_watch_forgets_total": 10, "room_watch_malformed_total": 11,
	}
	for key, v := range want {
		if g, ok := got[key]; !ok || g != v {
			t.Errorf("%s = %v (present %v), want %v", key, g, ok, v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("room cache sources = %v, want exactly %v", got, want)
	}
	if !gauges["room_tail_cache_bytes"] || gauges["room_cache_hits_total"] || gauges["room_watch_forgets_total"] {
		t.Errorf("gauge flags = %v, want only room_tail_cache_bytes as a gauge", gauges)
	}
}

func TestRoomCachesFollowTheConfig(t *testing.T) {
	caches, err := wireRoomCaches(config.RoomListConfig{JoinMax: 5, TailCacheBytes: 1 << 20, InfoCacheEntries: 3})
	if err != nil {
		t.Fatalf("wireRoomCaches: %v", err)
	}
	p := caches.probes(zeroWatch{})
	for _, name := range []string{"info", "tail", "snapshot", "path"} {
		if p.caches[name] == nil {
			t.Errorf("no hit counter for cache %s", name)
		}
	}
	if p.tailBytes() != 0 || p.forgets() != 0 || p.malformed() != 0 {
		t.Errorf("fresh caches report tail bytes %d, forgets %d, malformed %d; want zeros", p.tailBytes(), p.forgets(), p.malformed())
	}
	if _, err := wireRoomCaches(config.RoomListConfig{JoinMax: 5, TailCacheBytes: 1024, InfoCacheEntries: 3}); err == nil {
		t.Error("wireRoomCaches accepted a tail cache below one full tail")
	}
}
