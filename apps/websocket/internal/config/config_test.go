package config_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/config"
)

var envKeys = []string{
	"WS_NODE_ID", "WS_ADDR", "WS_ADMIN_ADDR", "REDIS_ADDR", "REDIS_DB", "REDIS_PASSWORD", "REDIS_PASSWORD_FILE",
	"WS_JWT_KEYS_FILE", "WS_JWT_ISSUER", "WS_JWT_AUDIENCE", "WS_JWT_LEEWAY",
	"WS_AUTH_TIMEOUT", "WS_AUTH_GRACE", "WS_AUTH_RATE", "WS_AUTH_BURST", "WS_AUTH_RETRY_MIN", "WS_AUTH_RETRY_MAX",
	"WS_USER_CONNECTIONS_MAX", "WS_UNAUTH_MAX", "WS_FRAME_MAX", "WS_HEARTBEAT_TIMEOUT",
	"WS_COMMAND_RATE", "WS_COMMAND_BURST", "WS_COMMAND_INFLIGHT", "WS_SEND_QUEUE", "WS_SEND_QUEUE_BYTES", "WS_WRITE_TIMEOUT",
	"CORE_REQUEST_DEADLINE", "WS_DRAIN_DELAY", "WS_SHUTDOWN_BUDGET",
	"NATS_URL", "EVT_STREAM", "EVT_SUBJECT_ROOT", "WS_PUBLISH_ROOT", "WS_PUBLISH_STREAM", "EVT_STREAM_REPLICAS",
	"WS_FANOUT_WORKERS", "WS_FANOUT_QUEUE", "WS_REPLAY_RATE", "WS_CONSUME_BATCH", "WS_CONSUME_BYTES",
	"WS_ROOM_IDS_INFLIGHT", "WS_OPEN_PENDING", "WS_REPLAY_MAX_AGE", "WS_RELOAD_RATE",
	"WS_TYPING_RATE", "WS_TYPING_BURST", "WS_TYPING_QUEUE", "WS_TYPING_PENDING_MSGS",
	"WS_PRESENCE_REDIS_ADDR", "WS_PRESENCE_REDIS_DB", "WS_PRESENCE_REDIS_PASSWORD", "WS_PRESENCE_REDIS_PASSWORD_FILE",
	"WS_PRESENCE_REDIS_TIMEOUT", "WS_PRESENCE_REDIS_POOL", "WS_PRESENCE_TTL", "WS_PRESENCE_STAMP_EVERY", "WS_PRESENCE_FLUSH", "WS_PRESENCE_BATCH",
	"WS_PRESENCE_CACHE", "WS_PRESENCE_WATCH_MAX",
}

var requiredEnv = map[string]string{
	"WS_NODE_ID":       "ws-a",
	"WS_JWT_KEYS_FILE": "/run/secrets/jwks.json",
	"WS_JWT_ISSUER":    "chatim-identity",
	"WS_JWT_AUDIENCE":  "chatim-websocket",
	"NATS_URL":         "nats://chatim-nats:4222",
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
	for k, v := range requiredEnv {
		t.Setenv(k, v)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, nil)
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		NodeID:             "ws-a",
		ListenAddr:         ":8080",
		AdminAddr:          ":9090",
		RedisAddr:          "chatim-redis:6379",
		RedisDB:            0,
		RedisPassword:      "",
		JWTKeysFile:        "/run/secrets/jwks.json",
		JWTIssuer:          "chatim-identity",
		JWTAudience:        "chatim-websocket",
		JWTLeeway:          30 * time.Second,
		AuthGrace:          3 * time.Minute,
		AuthRate:           1000,
		AuthBurst:          2000,
		AuthRetryMin:       5 * time.Second,
		AuthRetryMax:       30 * time.Second,
		UserConnectionsMax: 10,
		UnauthMax:          10000,
		FrameMax:           131072,
		HeartbeatTimeout:   15 * time.Second,
		CommandRate:        50,
		CommandBurst:       100,
		CommandInflight:    32,
		SendQueue:          256,
		SendQueueBytes:     1048576,
		WriteTimeout:       10 * time.Second,
		RequestDeadline:    5 * time.Second,
		DrainDelay:         2 * time.Second,
		ShutdownBudget:     15 * time.Second,
		NATSURL:            "nats://chatim-nats:4222",
		EventStream:        "CHATIM_EVT",
		EventRoot:          "evt",
		PublishRoot:        "websocket.publish",
		PublishStream:      "CHATIM_WS",
		StreamReplicas:     1,
		FanoutWorkers:      runtime.NumCPU(),
		FanoutQueue:        4096,
		ReplayRate:         20000,
		ConsumeBatch:       500,
		ConsumeBytes:       8 << 20,
		RoomIDsInflight:    64,
		OpenPending:        10 * time.Second,
		ReplayMaxAge:       2 * time.Minute,
		ReloadRate:         1000,
		TypingRate:         2,
		TypingBurst:        4,
		TypingQueue:        8192,
		TypingPendingMsgs:  65536,

		PresenceRedisAddr:    "chatim-redis-presence:6379",
		PresenceRedisTimeout: 500 * time.Millisecond,
		PresenceRedisPool:    64,
		PresenceTTL:          15 * time.Second,
		PresenceStampEvery:   2 * time.Second,
		PresenceFlush:        time.Second,
		PresenceBatch:        500,
		PresenceCache:        5 * time.Second,
		PresenceWatchMax:     200,
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", fields(got), fields(want))
	}
}

func fields(c config.Config) string {
	v := reflect.ValueOf(c)
	var b strings.Builder
	for i := range v.NumField() {
		fmt.Fprintf(&b, "%s=%v ", v.Type().Field(i).Name, v.Field(i).Interface())
	}
	return b.String()
}

func TestLoadReadsListenAddresses(t *testing.T) {
	setEnv(t, map[string]string{"WS_ADDR": "127.0.0.1:7080", "WS_ADMIN_ADDR": "127.0.0.1:7090"})
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ListenAddr != "127.0.0.1:7080" || got.AdminAddr != "127.0.0.1:7090" {
		t.Fatalf("ListenAddr, AdminAddr = %q, %q, want WS_ADDR and WS_ADMIN_ADDR", got.ListenAddr, got.AdminAddr)
	}
}

func TestLoadNodeIDDefaultsToHostname(t *testing.T) {
	setEnv(t, map[string]string{"WS_NODE_ID": ""})
	host, err := os.Hostname()
	if err != nil {
		t.Skipf("hostname: %v", err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.NodeID != host {
		t.Fatalf("NodeID = %q, want %q", got.NodeID, host)
	}
}

func TestLoadReadsRedisPasswordFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redis_password")
	if err := os.WriteFile(path, []byte("f1l3s3cr3t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, map[string]string{"REDIS_PASSWORD": "envs3cr3t", "REDIS_PASSWORD_FILE": path})
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.RedisPassword != "f1l3s3cr3t" {
		t.Fatalf("RedisPassword from file not used")
	}
}

func TestLoadReadsThePresenceRedisPasswordFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redis_presence_password")
	if err := os.WriteFile(path, []byte("pr3s3nc3f1l3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, map[string]string{"WS_PRESENCE_REDIS_PASSWORD": "envs3cr3t", "WS_PRESENCE_REDIS_PASSWORD_FILE": path, "WS_PRESENCE_REDIS_ADDR": "presence:6379", "WS_PRESENCE_REDIS_DB": "2"})
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.PresenceRedisPassword != "pr3s3nc3f1l3" || got.PresenceRedisAddr != "presence:6379" || got.PresenceRedisDB != 2 {
		t.Fatalf("presence redis from env not used: addr %q db %d", got.PresenceRedisAddr, got.PresenceRedisDB)
	}
	out := got.String()
	if strings.Contains(out, "pr3s3nc3f1l3") || !strings.Contains(out, "presence_redis_auth=true") {
		t.Fatalf("String() = %s, want presence_redis_auth=true without the password", out)
	}
}

func TestLoadFailsOnUnreadablePasswordFile(t *testing.T) {
	setEnv(t, map[string]string{"REDIS_PASSWORD_FILE": filepath.Join(t.TempDir(), "missing")})
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "REDIS_PASSWORD_FILE") {
		t.Fatalf("Load() = %v, want an error naming REDIS_PASSWORD_FILE", err)
	}
}

func TestLogValueHidesRedisPassword(t *testing.T) {
	setEnv(t, map[string]string{"REDIS_PASSWORD": "st4t3s3cr3t"})
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)
	logged := buf.String()
	for _, out := range []string{logged, fmt.Sprintf("%v %+v %s", cfg, cfg, cfg)} {
		if strings.Contains(out, "st4t3s3cr3t") {
			t.Fatalf("summary leaks the redis password: %s", out)
		}
	}
	for _, want := range []string{`"redis_auth":true`, `"node_id":"ws-a"`, `"listen_addr":":8080"`, `"frame_max":131072`, `"jwt_issuer":"chatim-identity"`} {
		if !strings.Contains(logged, want) {
			t.Errorf("summary %s misses %s", logged, want)
		}
	}
}

func TestLoadReadsTheFeedSettings(t *testing.T) {
	setEnv(t, map[string]string{
		"EVT_STREAM": "CHATIM_EVT_IT", "EVT_SUBJECT_ROOT": "evt_it", "WS_PUBLISH_ROOT": "ws_it.publish", "WS_PUBLISH_STREAM": "CHATIM_WS_IT",
		"EVT_STREAM_REPLICAS": "3", "WS_FANOUT_WORKERS": "6", "WS_FANOUT_QUEUE": "64", "WS_REPLAY_RATE": "900",
		"WS_CONSUME_BATCH": "50", "WS_CONSUME_BYTES": "2097152", "WS_ROOM_IDS_INFLIGHT": "8", "WS_OPEN_PENDING": "3s",
		"WS_REPLAY_MAX_AGE": "90s", "WS_RELOAD_RATE": "250",
	})
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.EventStream != "CHATIM_EVT_IT" || got.EventRoot != "evt_it" || got.PublishRoot != "ws_it.publish" || got.PublishStream != "CHATIM_WS_IT" ||
		got.StreamReplicas != 3 || got.FanoutWorkers != 6 || got.FanoutQueue != 64 || got.ReplayRate != 900 ||
		got.ConsumeBatch != 50 || got.ConsumeBytes != 2<<20 || got.RoomIDsInflight != 8 || got.OpenPending != 3*time.Second ||
		got.ReplayMaxAge != 90*time.Second || got.ReloadRate != 250 {
		t.Fatalf("Load() = %+v", fields(got))
	}
}

func TestLogValueHidesNATSCredentials(t *testing.T) {
	setEnv(t, map[string]string{"NATS_URL": "nats://ws:n4tss3cr3t@chatim-nats:4222"})
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := cfg.String()
	if strings.Contains(out, "n4tss3cr3t") || !strings.Contains(out, "chatim-nats:4222") {
		t.Fatalf("String() = %s, want the NATS host without the password", out)
	}
}

func TestLogValueReportsRedisWithoutAuth(t *testing.T) {
	setEnv(t, nil)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out := cfg.String(); !strings.Contains(out, "redis_auth=false") {
		t.Fatalf("String() = %s, want redis_auth=false", out)
	}
}

func TestLogValueCoversEveryField(t *testing.T) {
	cfg := config.Config{}
	attrs := cfg.LogValue().Group()
	if got, want := len(attrs), reflect.TypeFor[config.Config]().NumField(); got != want {
		t.Fatalf("LogValue has %d attrs, Config has %d fields", got, want)
	}
}
