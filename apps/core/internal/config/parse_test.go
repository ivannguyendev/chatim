package config_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
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
		{"EVT_STREAM_REPLICAS", "0"},
		{"EVT_STREAM_MAX_AGE", "0s"},
		{"EVT_STREAM_DUPLICATES", "0s"},
		{"SLOT_TICK", "0s"},
		{"SLOT_HEARTBEAT_TTL", "0s"},
		{"SLOT_LEASE_TTL", "0s"},
		{"SLOT_HOOK_TIMEOUT", "0s"},
		{"PIN_LIMIT", "0"},
		{"REACTION_COUNT_DELAY", "0s"},
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

func TestLoadReadsLockedMessageKinds(t *testing.T) {
	tests := []struct {
		value string
		want  []domain.Kind
	}{
		{"", nil},
		{" text , ", []domain.Kind{domain.KindText}},
		{",text,,text", []domain.Kind{domain.KindText, domain.KindText}},
	}
	for _, tt := range tests {
		setEnv(t, map[string]string{"MESSAGE_LOCKED_KINDS": tt.value})
		got, err := config.Load()
		if err != nil || !slices.Equal(got.LockedMessageKinds, tt.want) {
			t.Fatalf("MESSAGE_LOCKED_KINDS=%q gives %v, %v; want %v", tt.value, got.LockedMessageKinds, err, tt.want)
		}
	}
}

func TestLoadRejectsAnUnknownLockedMessageKind(t *testing.T) {
	setEnv(t, map[string]string{"MESSAGE_LOCKED_KINDS": "text,nope"})
	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "MESSAGE_LOCKED_KINDS") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("Load() = %v, want an error naming MESSAGE_LOCKED_KINDS and nope", err)
	}
}

func TestLoadReadsReactionEmojis(t *testing.T) {
	tests := []struct {
		value string
		want  []string
	}{
		{"", []string{"👍", "❤️", "😂", "😮", "😢", "🙏"}},
		{" , ", []string{"👍", "❤️", "😂", "😮", "😢", "🙏"}},
		{" 🎉 , 👍 ,", []string{"🎉", "👍"}},
	}
	for _, tt := range tests {
		setEnv(t, map[string]string{"REACTION_EMOJIS": tt.value})
		got, err := config.Load()
		if err != nil || !slices.Equal(got.Limits.Emojis, tt.want) {
			t.Fatalf("REACTION_EMOJIS=%q gives %q, %v; want %q", tt.value, got.Limits.Emojis, err, tt.want)
		}
	}
}

func TestLoadRejectsBadReactionEmojis(t *testing.T) {
	var many []string
	for i := range 101 {
		many = append(many, "e"+strconv.Itoa(i))
	}
	tests := []struct {
		value, bad string
	}{
		{"👍,❤️,👍", "👍"},
		{"👍," + strings.Repeat("x", 33), strings.Repeat("x", 33)},
		{strings.Join(many, ","), "101"},
	}
	for _, tt := range tests {
		setEnv(t, map[string]string{"REACTION_EMOJIS": tt.value})
		_, err := config.Load()
		if err == nil || !strings.Contains(err.Error(), "REACTION_EMOJIS") || !strings.Contains(err.Error(), tt.bad) {
			t.Fatalf("REACTION_EMOJIS=%q: Load() = %v, want an error naming REACTION_EMOJIS and %q", tt.value, err, tt.bad)
		}
	}
	setEnv(t, map[string]string{"REACTION_EMOJIS": strings.Join(many[:100], ",")})
	if _, err := config.Load(); err != nil {
		t.Fatalf("100 emojis: Load() = %v, want nil", err)
	}
}
