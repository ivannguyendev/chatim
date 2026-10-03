package config_test

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func TestLoadJoinsAllParseErrors(t *testing.T) {
	setEnv(t, map[string]string{
		"REDIS_DB": "x", "FLUSH_WINDOW": "fast", "FLUSH_SHARDS": "four", "CORE_GRPC_SHUTDOWN": "10",
		"PUB_QUEUE": "many", "SLOT_TICK": "1", "CID_PENDING_TTL": "ten", "CORE_GRPC_ADDR": "9000",
	})
	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil, want parse errors")
	}
	for _, key := range []string{"REDIS_DB", "FLUSH_WINDOW", "FLUSH_SHARDS", "CORE_GRPC_SHUTDOWN", "PUB_QUEUE", "SLOT_TICK", "CID_PENDING_TTL", "CORE_GRPC_ADDR"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("Load() error %q does not mention %s", err, key)
		}
	}
}

func TestLoadRejectsNonPositiveValues(t *testing.T) {
	tests := []struct{ key, value string }{
		{"REDIS_DB", "-1"},
		{"REDIS_DEDUPE_DB", "-1"},
		{"CORE_MAX_INFLIGHT", "0"},
		{"CORE_REQUEST_DEADLINE", "0s"},
		{"CORE_SLOW_RPC", "0s"},
		{"CORE_CONNECT_TIMEOUT", "-1s"},
		{"CORE_QUEUE_WAIT", "0s"},
		{"CORE_DRAIN_DELAY", "0s"},
		{"CORE_GRPC_SHUTDOWN", "0s"},
		{"CORE_PUBLISHER_DRAIN", "0s"},
		{"CORE_SHUTDOWN_BUDGET", "0s"},
		{"FLUSH_SHARDS", "-1"},
		{"FLUSH_MAX_BATCH", "0"},
		{"FLUSH_QUEUE", "0"},
		{"FLUSH_WINDOW", "0s"},
		{"FLUSH_INSERT_TIMEOUT", "0s"},
		{"ACTOR_MAILBOX", "0"},
		{"ACTOR_IDLE", "-1s"},
		{"ACTOR_MAX_GROUP", "0"},
		{"ACTOR_MAX", "0"},
		{"CID_PENDING_TTL", "0s"},
		{"CID_COMMITTED_TTL", "0s"},
		{"REDIS_OP_TIMEOUT", "0s"},
		{"REDIS_COOLDOWN", "0s"},
		{"PUB_SHARDS", "0"},
		{"PUB_QUEUE", "0"},
		{"PUB_MAX_PENDING", "0"},
		{"PUB_ACK_TIMEOUT", "0s"},
		{"PUB_FLUSH_EVERY", "0s"},
		{"PUB_WATERMARK_TTL", "0s"},
		{"EVT_STREAM_REPLICAS", "0"},
		{"EVT_STREAM_MAX_AGE", "0s"},
		{"EVT_STREAM_DUPLICATES", "0s"},
		{"SLOT_TICK", "0s"},
		{"SLOT_HEARTBEAT_TTL", "0s"},
		{"SLOT_LEASE_TTL", "0s"},
		{"SLOT_HOOK_TIMEOUT", "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setEnv(t, map[string]string{tt.key: tt.value})
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("Load() with %s=%s = %v, want an error naming %s", tt.key, tt.value, err, tt.key)
			}
		})
	}
}
