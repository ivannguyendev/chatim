package main

import (
	"cmp"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

const (
	itRedisPasswordEnv       = "CHATIM_IT_REDIS_PASSWORD"
	itRedisDedupeAddrEnv     = "CHATIM_IT_REDIS_DEDUPE_ADDR"
	itRedisDedupePasswordEnv = "CHATIM_IT_REDIS_DEDUPE_PASSWORD"
)

type itRedisTarget struct {
	instance, addr, password string
}

func itRedisTargets() (state, dedupe itRedisTarget) {
	state = itRedisTarget{"state", os.Getenv(itRedisAddrEnv), os.Getenv(itRedisPasswordEnv)}
	dedupe = state
	dedupe.instance = "dedupe"
	if addr := os.Getenv(itRedisDedupeAddrEnv); addr != "" {
		dedupe.addr, dedupe.password = addr, os.Getenv(itRedisDedupePasswordEnv)
	}
	return state, dedupe
}

func itRedisClient(t *testing.T, target itRedisTarget, db int) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: target.addr, DB: db, Password: target.password, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("redis %s ping %s: %v", target.instance, target.addr, err)
	}
	return rdb
}

func TestRealRedisRejectsClientsWithoutThePassword(t *testing.T) {
	state, dedupe := itRedisTargets()
	if state.addr == "" || cmp.Or(state.password, dedupe.password) == "" {
		t.Skip("set CHATIM_IT_REDIS_ADDR with CHATIM_IT_REDIS_PASSWORD or CHATIM_IT_REDIS_DEDUPE_PASSWORD to run")
	}
	for _, target := range []itRedisTarget{state, dedupe} {
		if target.password == "" {
			continue
		}
		t.Run(target.instance, func(t *testing.T) {
			anonymous := redis.NewClient(&redis.Options{Addr: target.addr, ContextTimeoutEnabled: true, MaxRetries: -1})
			defer anonymous.Close()
			err := anonymous.Ping(t.Context()).Err()
			if err == nil || !strings.HasPrefix(err.Error(), "NOAUTH") {
				t.Fatalf("ping without a password = %v, want NOAUTH", err)
			}
			itRedisClient(t, target, 0)
		})
	}
}
