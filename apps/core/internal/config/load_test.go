package config_test

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/slot"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const day = 24 * time.Hour

func TestLoadDefaults(t *testing.T) {
	setEnv(t, nil)
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		CoreID: host, GRPCAddr: ":9000", AdvertiseAddr: host + ":9000", AdminAddr: ":9090",
		MongoURI: testMongoURI, MongoDB: "chatim", MongoAuthSource: "admin", RedisAddr: "chatim-redis:6379", RedisDB: 0,
		RedisDedupeAddr: "chatim-redis-dedupe:6379",
		NATSURL:         "nats://chatim-nats:4222", ConnectTimeout: 10 * time.Second, RequestDeadline: 3 * time.Second,
		SlowRPC: 500 * time.Millisecond, QueueWait: 25 * time.Millisecond, MaxInflight: 2048, DrainDelay: 2 * time.Second, GRPCShutdown: 5 * time.Second,
		PublisherDrain: 5 * time.Second, ShutdownBudget: 28 * time.Second,
		Flush: flush.Config{Shards: 4, Window: 2 * time.Millisecond, MaxBatch: 256, QueueSize: 1024, InsertTimeout: time.Second},
		Actor: actor.Config{
			Mailbox: 1024, Idle: 5 * time.Minute, MaxGroup: 64, MaxActors: 100000,
			GroupDeadline: 3 * time.Second, ReservationTTL: 10 * time.Second,
		},
		Dedupe:   dedupe.Config{CoreID: host, PendingTTL: 10 * time.Second, CommittedTTL: 15 * time.Minute, Timeout: 100 * time.Millisecond, Cooldown: time.Second},
		CIDBatch: dedupe.BatchConfig{Shards: 4, MaxKeys: 256, Queue: 4096},
		Publish: publish.Config{
			SubjectRoot: "evt", Shards: 4, QueueSize: 1024, MaxPending: 256, AckTimeout: 2 * time.Second,
		},
		Stream: publish.StreamConfig{Name: "CHATIM_EVT", SubjectRoot: "evt", LiveRoot: "live", Replicas: 1, MaxAge: 7 * day, Duplicates: 5 * time.Minute},
		Work:   work.StreamConfig{Name: "CHATIM_WORK", SubjectRoot: "work", Partitions: 32, Replicas: 1, MaxAge: 2 * time.Hour, Duplicates: 2 * time.Minute, AckWait: 35 * time.Second},
		Slot: slot.Config{
			CoreID: host, Addr: host + ":9000", Tick: time.Second, HeartbeatTTL: 5 * time.Second,
			LeaseTTL: 10 * time.Second, HookTimeout: 500 * time.Millisecond,
		},
		ReconcileEnabled: true,
		Reconcile: reconcile.Config{
			SubjectRoot: "work", Partitions: 32, Window: 1024, Batch: 256,
			ConfirmEvery: time.Second, Drain: time.Second, Poll: time.Second,
		},
		EffectDelay:        5 * time.Second,
		EffectRoomCache:    65536,
		Effects:            effects.Config{Partitions: 32, FetchBatch: 256, FetchWait: time.Second, RetryDelay: 5 * time.Second, Drain: time.Second, Poll: time.Second},
		AckMarks:           eventmark.Config{TTL: time.Hour, Timeout: 100 * time.Millisecond, Cooldown: time.Second},
		Limits:             mutate.Limits{MaxEmojis: 20, PinLimit: 50},
		ReactionCountDelay: time.Second,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() =\n%#v\nwant\n%#v", got, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	setEnv(t, overrides)
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		CoreID: "core-a", GRPCAddr: ":7000", AdvertiseAddr: "10.0.0.5:7000", AdminAddr: "127.0.0.1:7090",
		MongoURI: "mongodb://m1,m2/?replicaSet=rs1", MongoDB: "chatim_it",
		MongoUser: "core-user", MongoPassword: "mongo-pw", MongoAuthSource: "chatim", RedisAddr: "redis:6380", RedisDB: 3,
		RedisPassword: "state-pw", RedisDedupeAddr: "dedupe:6381", RedisDedupeDB: 2, RedisDedupePassword: "dedupe-pw",
		NATSURL: "nats://n1:4222", ConnectTimeout: 4 * time.Second, RequestDeadline: 2 * time.Second,
		SlowRPC: 750 * time.Millisecond, QueueWait: 10 * time.Millisecond, MaxInflight: 100, DrainDelay: time.Second, GRPCShutdown: 4 * time.Second,
		PublisherDrain: 3 * time.Second, ShutdownBudget: 20 * time.Second,
		Flush: flush.Config{Shards: 8, Window: 5 * time.Millisecond, MaxBatch: 512, QueueSize: 256, InsertTimeout: 500 * time.Millisecond},
		Actor: actor.Config{
			Mailbox: 64, Idle: time.Minute, MaxGroup: 32, MaxActors: 5000,
			GroupDeadline: 2 * time.Second, ReservationTTL: 8 * time.Second,
		},
		Dedupe:   dedupe.Config{CoreID: "core-a", PendingTTL: 8 * time.Second, CommittedTTL: 30 * time.Minute, Timeout: 50 * time.Millisecond, Cooldown: 2 * time.Second},
		CIDBatch: dedupe.BatchConfig{Shards: 2, MaxKeys: 64, Queue: 512},
		Publish: publish.Config{
			SubjectRoot: "evt_it", Shards: 2, QueueSize: 512, MaxPending: 128, AckTimeout: time.Second,
		},
		Stream: publish.StreamConfig{Name: "CHATIM_EVT_IT", SubjectRoot: "evt_it", LiveRoot: "live_it", Replicas: 3, MaxAge: 2 * day, Duplicates: 5 * time.Minute},
		Work:   work.StreamConfig{Name: "CHATIM_WORK_IT", SubjectRoot: "work_it", Partitions: 16, Replicas: 3, MaxAge: time.Hour, Duplicates: 3 * time.Minute, AckWait: 50 * time.Second},
		Slot: slot.Config{
			CoreID: "core-a", Addr: "10.0.0.5:7000", Tick: 500 * time.Millisecond, HeartbeatTTL: 3 * time.Second,
			LeaseTTL: 6 * time.Second, HookTimeout: 200 * time.Millisecond,
		},
		ReconcileEnabled: false,
		Reconcile: reconcile.Config{
			SubjectRoot: "work_it", Partitions: 16, Window: 64, Batch: 32,
			ConfirmEvery: 2 * time.Second, Drain: 500 * time.Millisecond, Poll: 500 * time.Millisecond,
		},
		EffectDelay:        20 * time.Second,
		EffectRoomCache:    128,
		Effects:            effects.Config{Partitions: 16, FetchBatch: 64, FetchWait: 500 * time.Millisecond, RetryDelay: 2 * time.Second, Drain: 500 * time.Millisecond, Poll: 500 * time.Millisecond},
		AckMarks:           eventmark.Config{TTL: 30 * time.Minute, Timeout: 50 * time.Millisecond, Cooldown: 2 * time.Second},
		LockedMessageKinds: []domain.Kind{domain.KindText},
		Limits:             mutate.Limits{MaxEmojis: 30, PinLimit: 10},
		ReactionCountDelay: 2 * time.Second,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() =\n%#v\nwant\n%#v", got, want)
	}
}

func TestOverridesCoverEveryKey(t *testing.T) {
	for _, k := range envKeys {
		if _, ok := overrides[k]; !ok {
			t.Errorf("overrides miss %s", k)
		}
	}
	if len(overrides) != len(envKeys) {
		t.Errorf("overrides has %d keys, envKeys %d", len(overrides), len(envKeys))
	}
}

func TestLoadAdvertiseAddrFollowsCoreIDAndGRPCPort(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default port", map[string]string{"CORE_ID": "core-b"}, "core-b:9000"},
		{"port of a bare grpc addr", map[string]string{"CORE_ID": "core-b", "CORE_GRPC_ADDR": ":7100"}, "core-b:7100"},
		{"port of a bound grpc addr", map[string]string{"CORE_ID": "core-b", "CORE_GRPC_ADDR": "0.0.0.0:7200"}, "core-b:7200"},
		{"explicit advertise addr wins", map[string]string{"CORE_ID": "core-b", "CORE_ADVERTISE_ADDR": "10.1.1.1:7300"}, "10.1.1.1:7300"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			got, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got.AdvertiseAddr != tt.want || got.Slot.Addr != tt.want {
				t.Errorf("AdvertiseAddr = %q, Slot.Addr = %q, want %q", got.AdvertiseAddr, got.Slot.Addr, tt.want)
			}
		})
	}
}

func TestLoadDerivesHookTimeoutFromTick(t *testing.T) {
	setEnv(t, map[string]string{"SLOT_TICK": "600ms"})
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Slot.HookTimeout != 300*time.Millisecond {
		t.Errorf("Slot.HookTimeout = %v, want half of SLOT_TICK", got.Slot.HookTimeout)
	}
}

func TestAdminAddrReadsOnlyTheAdminKey(t *testing.T) {
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
	if got := config.AdminAddr(); got != ":9090" {
		t.Errorf("AdminAddr() = %q, want :9090", got)
	}
	t.Setenv("CORE_ADMIN_ADDR", "127.0.0.1:7091")
	if got := config.AdminAddr(); got != "127.0.0.1:7091" {
		t.Errorf("AdminAddr() = %q, want 127.0.0.1:7091", got)
	}
}
