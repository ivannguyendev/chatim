package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

var envKeys = []string{
	"CORE_ID", "CORE_GRPC_ADDR", "CORE_ADVERTISE_ADDR", "CORE_ADMIN_ADDR",
	"MONGO_URI", "MONGO_DB", "REDIS_ADDR", "REDIS_DB", "NATS_URL",
	"EVT_STREAM", "EVT_SUBJECT_ROOT", "EVT_LIVE_ROOT", "EVT_STREAM_REPLICAS",
	"FLUSH_WINDOW", "FLUSH_MAX_BATCH", "FLUSH_SHARDS", "ACTOR_MAILBOX", "ACTOR_IDLE",
	"CORE_REQUEST_DEADLINE", "CORE_MAX_INFLIGHT", "CORE_DRAIN_DELAY",
	"CORE_GRPC_SHUTDOWN", "CORE_PUBLISHER_DRAIN", "CORE_SHUTDOWN_BUDGET",
}

const testMongoURI = "mongodb://chatim-mongodb:27017/?replicaSet=rs0"

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
	t.Setenv("MONGO_URI", testMongoURI)
	for k, v := range env {
		t.Setenv(k, v)
	}
}

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
		MongoURI: testMongoURI, MongoDB: "chatim", RedisAddr: "chatim-redis:6379", RedisDB: 0,
		NATSURL: "nats://chatim-nats:4222", StreamName: "CHATIM_EVT", SubjectRoot: "evt", LiveRoot: "live",
		StreamReplicas: 1, FlushWindow: 2 * time.Millisecond, FlushMaxBatch: 256, FlushShards: 4,
		Mailbox: 1024, ActorIdle: 5 * time.Minute, RequestDeadline: 3 * time.Second, MaxInflight: 2048,
		DrainDelay: 2 * time.Second, GRPCShutdown: 10 * time.Second, PublisherDrain: 5 * time.Second,
		ShutdownBudget: 25 * time.Second,
	}
	if got != want {
		t.Errorf("Load() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"CORE_ID": "core-a", "CORE_GRPC_ADDR": ":7000", "CORE_ADVERTISE_ADDR": "10.0.0.5:7000",
		"CORE_ADMIN_ADDR": "127.0.0.1:7090", "MONGO_URI": "mongodb://m1,m2/?replicaSet=rs1",
		"MONGO_DB": "chatim_it", "REDIS_ADDR": "redis:6380", "REDIS_DB": "3", "NATS_URL": "nats://n1:4222",
		"EVT_STREAM": "CHATIM_EVT_IT", "EVT_SUBJECT_ROOT": "evt_it", "EVT_LIVE_ROOT": "live_it",
		"EVT_STREAM_REPLICAS": "3", "FLUSH_WINDOW": "5ms", "FLUSH_MAX_BATCH": "512", "FLUSH_SHARDS": "8",
		"ACTOR_MAILBOX": "64", "ACTOR_IDLE": "1m", "CORE_REQUEST_DEADLINE": "2s", "CORE_MAX_INFLIGHT": "100",
		"CORE_DRAIN_DELAY": "1s", "CORE_GRPC_SHUTDOWN": "5s", "CORE_PUBLISHER_DRAIN": "3s",
		"CORE_SHUTDOWN_BUDGET": "20s",
	})
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		CoreID: "core-a", GRPCAddr: ":7000", AdvertiseAddr: "10.0.0.5:7000", AdminAddr: "127.0.0.1:7090",
		MongoURI: "mongodb://m1,m2/?replicaSet=rs1", MongoDB: "chatim_it", RedisAddr: "redis:6380", RedisDB: 3,
		NATSURL: "nats://n1:4222", StreamName: "CHATIM_EVT_IT", SubjectRoot: "evt_it", LiveRoot: "live_it",
		StreamReplicas: 3, FlushWindow: 5 * time.Millisecond, FlushMaxBatch: 512, FlushShards: 8,
		Mailbox: 64, ActorIdle: time.Minute, RequestDeadline: 2 * time.Second, MaxInflight: 100,
		DrainDelay: time.Second, GRPCShutdown: 5 * time.Second, PublisherDrain: 3 * time.Second,
		ShutdownBudget: 20 * time.Second,
	}
	if got != want {
		t.Errorf("Load() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestLoadAdvertiseAddrFollowsCoreID(t *testing.T) {
	setEnv(t, map[string]string{"CORE_ID": "core-b"})
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AdvertiseAddr != "core-b:9000" {
		t.Errorf("AdvertiseAddr = %q, want %q", got.AdvertiseAddr, "core-b:9000")
	}
}

func TestLoadJoinsAllParseErrors(t *testing.T) {
	setEnv(t, map[string]string{"REDIS_DB": "x", "FLUSH_WINDOW": "fast", "FLUSH_SHARDS": "four", "CORE_GRPC_SHUTDOWN": "10"})
	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil, want parse errors")
	}
	for _, key := range []string{"REDIS_DB", "FLUSH_WINDOW", "FLUSH_SHARDS", "CORE_GRPC_SHUTDOWN"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("Load() error %q does not mention %s", err, key)
		}
	}
}
