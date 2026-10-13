package config_test

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func TestRoomListKnobsMustBePositive(t *testing.T) {
	for _, key := range []string{"ROOM_LIST_JOIN_MAX", "ROOM_TAIL_CACHE_BYTES", "ROOM_INFO_CACHE_ENTRIES"} {
		for _, value := range []string{"0", "-1", "many"} {
			setEnv(t, map[string]string{key: value})
			if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("%s=%s: Load() = %v, want an error naming %s", key, value, err, key)
			}
		}
	}
}

func TestRoomListKnobsAcceptTheirSmallestValues(t *testing.T) {
	setEnv(t, map[string]string{"ROOM_LIST_JOIN_MAX": "1", "ROOM_TAIL_CACHE_BYTES": "1048576", "ROOM_INFO_CACHE_ENTRIES": "1"})
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	want := config.RoomListConfig{JoinMax: 1, TailCacheBytes: 1 << 20, InfoCacheEntries: 1}
	if got.RoomList != want {
		t.Fatalf("RoomList = %+v, want %+v", got.RoomList, want)
	}
}
