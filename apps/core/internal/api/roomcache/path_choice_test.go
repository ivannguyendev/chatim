package roomcache

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestChoosePathSwitchesAboveTheJoinMax(t *testing.T) {
	if ChoosePath(2000, 2000) != PathJoin || ChoosePath(2001, 2000) != PathApp || ChoosePath(0, 2000) != PathJoin {
		t.Error("ChoosePath does not pick join up to the max and app above it")
	}
}

func TestPathCacheExpiresAndKeepsTenantsApart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := NewPathCache(10)
		if err != nil {
			t.Fatalf("NewPathCache: %v", err)
		}
		c.Put("acme", "minh", PathApp)
		if _, ok := c.Get("other", "minh"); ok {
			t.Error("another tenant read minh's path")
		}
		time.Sleep(PathTTL - time.Millisecond)
		if p, ok := c.Get("acme", "minh"); !ok || p != PathApp {
			t.Fatalf("Get = %d, %v before the TTL, want app", p, ok)
		}
		time.Sleep(time.Millisecond)
		if p, ok := c.Get("acme", "minh"); ok || p != 0 {
			t.Errorf("Get = %d, %v after the TTL, want a miss", p, ok)
		}
		expectStats(t, "path", c, 1, 2)
	})
}
