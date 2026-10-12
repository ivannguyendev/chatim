package app

import (
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func authedRedis(t *testing.T, password string) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.RequireAuth(password)
	return mr
}

func redisConfig(state, dedupe *miniredis.Miniredis, statePW, dedupePW string) config.Config {
	return config.Config{
		CoreID: "core-redis", ConnectTimeout: time.Second,
		RedisAddr: state.Addr(), RedisDB: 0, RedisPassword: statePW,
		RedisDedupeAddr: dedupe.Addr(), RedisDedupeDB: 0, RedisDedupePassword: dedupePW,
	}
}

func TestConnectRedisSplitsStateAndDedupe(t *testing.T) {
	state, dedupe := authedRedis(t, "state-pw"), authedRedis(t, "dedupe-pw")
	c := &clients{}
	defer c.close(t.Context(), quiet)
	if err := c.connectRedis(t.Context(), redisConfig(state, dedupe, "state-pw", "dedupe-pw")); err != nil {
		t.Fatalf("connectRedis: %v", err)
	}
	if err := c.dedupe.Set(t.Context(), "chatim:cid:probe", "1", 0).Err(); err != nil {
		t.Fatalf("set on dedupe: %v", err)
	}
	if err := c.slots.Set(t.Context(), "chatim:slot:1", "core-redis", 0).Err(); err != nil {
		t.Fatalf("set on slots: %v", err)
	}
	if !dedupe.Exists("chatim:cid:probe") || state.Exists("chatim:cid:probe") {
		t.Error("cid key did not land on the dedupe instance only")
	}
	if !state.Exists("chatim:slot:1") || dedupe.Exists("chatim:slot:1") {
		t.Error("slot key did not land on the state instance only")
	}
}

func TestConnectRedisNamesTheFailingInstanceWithoutPasswords(t *testing.T) {
	state, dedupe := authedRedis(t, "state-pw"), authedRedis(t, "dedupe-pw")
	tests := []struct {
		name, statePW, dedupePW, want string
	}{
		{"state", "wrong-state-pw", "dedupe-pw", "redis state ping " + state.Addr() + "/0"},
		{"dedupe", "state-pw", "wrong-dedupe-pw", "redis dedupe ping " + dedupe.Addr() + "/0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &clients{}
			defer c.close(t.Context(), quiet)
			err := c.connectRedis(t.Context(), redisConfig(state, dedupe, tt.statePW, tt.dedupePW))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("connectRedis = %v, want an error containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "pw") {
				t.Errorf("error leaks a password: %v", err)
			}
		})
	}
}

func TestRedisClientsUseSmallWarmPools(t *testing.T) {
	state, dedupe := authedRedis(t, "state-pw"), authedRedis(t, "dedupe-pw")
	cfg := redisConfig(state, dedupe, "state-pw", "dedupe-pw")
	cfg.CIDBatch.Shards = 3
	c := &clients{}
	defer c.close(t.Context(), quiet)
	if err := c.connectRedis(t.Context(), cfg); err != nil {
		t.Fatalf("connectRedis: %v", err)
	}
	d := c.dedupe.Options()
	if d.PoolSize != 10 || d.MaxActiveConns != 10 || d.MinIdleConns != 6 || d.MaxRetries != 0 || d.DialerRetries != 1 || !d.DisableIdentity {
		t.Errorf("dedupe pool %d/%d idle %d retries %d dial retries %d no identity %v, want 10/10 idle 6 retries 0 dial retries 1 no identity",
			d.PoolSize, d.MaxActiveConns, d.MinIdleConns, d.MaxRetries, d.DialerRetries, d.DisableIdentity)
	}
	s := c.slots.Options()
	if s.PoolSize != 4 || s.MaxRetries != 3 || s.DialerRetries != 5 || !s.DisableIdentity {
		t.Errorf("slots pool %d retries %d dial retries %d no identity %v, want 4, 3, 5 and no identity",
			s.PoolSize, s.MaxRetries, s.DialerRetries, s.DisableIdentity)
	}
}
