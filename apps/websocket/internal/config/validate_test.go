package config_test

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/config"
)

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"defaults are valid", nil, ""},
		{"missing keys file", map[string]string{"WS_JWT_KEYS_FILE": ""}, "WS_JWT_KEYS_FILE is required"},
		{"missing issuer", map[string]string{"WS_JWT_ISSUER": ""}, "WS_JWT_ISSUER is required"},
		{"missing audience", map[string]string{"WS_JWT_AUDIENCE": ""}, "WS_JWT_AUDIENCE is required"},
		{"unparsable count", map[string]string{"WS_UNAUTH_MAX": "many"}, "WS_UNAUTH_MAX"},
		{"unparsable duration", map[string]string{"WS_AUTH_GRACE": "soon"}, "WS_AUTH_GRACE"},
		{"old auth timeout ignored", map[string]string{"WS_AUTH_TIMEOUT": "soon"}, ""},
		{"negative redis db", map[string]string{"REDIS_DB": "-1"}, "REDIS_DB must not be negative"},
		{"zero leeway", map[string]string{"WS_JWT_LEEWAY": "0s"}, ""},
		{"negative leeway", map[string]string{"WS_JWT_LEEWAY": "-1s"}, "WS_JWT_LEEWAY must not be negative"},
		{"leeway over a minute", map[string]string{"WS_JWT_LEEWAY": "61s"}, "WS_JWT_LEEWAY must not exceed 1m0s"},
		{"iam internal url without secret", map[string]string{"WS_IAM_INTERNAL_URL": "http://chatim-iam-1:8081"}, "node secret is required"},
		{"iam jwks over http", map[string]string{"WS_IAM_JWKS_URL": "http://chatim-iam-1:8080/.well-known/jwks.json"}, "jwks url must be an absolute https url"},
		{"iam jwks over http in dev", map[string]string{"WS_IAM_JWKS_URL": "http://chatim-iam-1:8080/.well-known/jwks.json", "WS_IAM_ALLOW_HTTP": "true"}, ""},
		{"bad iam interval", map[string]string{"WS_IAM_REVOCATIONS_INTERVAL": "soon"}, "WS_IAM_REVOCATIONS_INTERVAL"},
		{"zero auth grace", map[string]string{"WS_AUTH_GRACE": "0s"}, "WS_AUTH_GRACE must be positive"},
		{"zero auth rate", map[string]string{"WS_AUTH_RATE": "0"}, "WS_AUTH_RATE must be positive"},
		{"zero auth burst", map[string]string{"WS_AUTH_BURST": "0"}, "WS_AUTH_BURST must be positive"},
		{"zero auth retry min", map[string]string{"WS_AUTH_RETRY_MIN": "0s"}, "WS_AUTH_RETRY_MIN must be positive"},
		{"zero auth retry max", map[string]string{"WS_AUTH_RETRY_MAX": "0s", "WS_AUTH_RETRY_MIN": "1ms"}, "WS_AUTH_RETRY_MAX must be positive"},
		{"auth retry min over max", map[string]string{"WS_AUTH_RETRY_MIN": "31s"}, "WS_AUTH_RETRY_MIN 31s must not exceed WS_AUTH_RETRY_MAX 30s"},
		{"auth retry min at max", map[string]string{"WS_AUTH_RETRY_MIN": "30s"}, ""},
		{"zero user connections", map[string]string{"WS_USER_CONNECTIONS_MAX": "0"}, "WS_USER_CONNECTIONS_MAX must be positive"},
		{"zero unauth max", map[string]string{"WS_UNAUTH_MAX": "0"}, "WS_UNAUTH_MAX must be positive"},
		{"negative frame max", map[string]string{"WS_FRAME_MAX": "-1"}, "WS_FRAME_MAX must be positive"},
		{"zero command rate", map[string]string{"WS_COMMAND_RATE": "0"}, "WS_COMMAND_RATE must be positive"},
		{"zero command burst", map[string]string{"WS_COMMAND_BURST": "0"}, "WS_COMMAND_BURST must be positive"},
		{"zero command inflight", map[string]string{"WS_COMMAND_INFLIGHT": "0"}, "WS_COMMAND_INFLIGHT must be positive"},
		{"zero send queue", map[string]string{"WS_SEND_QUEUE": "0"}, "WS_SEND_QUEUE must be positive"},
		{"send queue of one frame", map[string]string{"WS_SEND_QUEUE": "1"}, "WS_SEND_QUEUE must be at least 2"},
		{"send queue of two frames", map[string]string{"WS_SEND_QUEUE": "2"}, ""},
		{"zero send queue bytes", map[string]string{"WS_SEND_QUEUE_BYTES": "0"}, "WS_SEND_QUEUE_BYTES must be positive"},
		{"send queue bytes under one frame", map[string]string{"WS_SEND_QUEUE_BYTES": "131071"}, "WS_SEND_QUEUE_BYTES 131071 must be at least WS_FRAME_MAX 131072"},
		{"send queue bytes at one frame", map[string]string{"WS_SEND_QUEUE_BYTES": "131072"}, ""},
		{"zero write timeout", map[string]string{"WS_WRITE_TIMEOUT": "0s"}, "WS_WRITE_TIMEOUT must be positive"},
		{"zero request deadline", map[string]string{"CORE_REQUEST_DEADLINE": "0s"}, "CORE_REQUEST_DEADLINE must be positive"},
		{"zero drain delay", map[string]string{"WS_DRAIN_DELAY": "0s"}, "WS_DRAIN_DELAY must be positive"},
		{"zero shutdown budget", map[string]string{"WS_SHUTDOWN_BUDGET": "0s"}, "WS_SHUTDOWN_BUDGET must be positive"},
		{"frame max under 4KB", map[string]string{"WS_FRAME_MAX": "4095"}, "WS_FRAME_MAX must be at least 4096"},
		{"frame max at 4KB", map[string]string{"WS_FRAME_MAX": "4096"}, ""},
		{"heartbeat timeout under two heartbeats", map[string]string{"WS_HEARTBEAT_TIMEOUT": "9999ms"}, "WS_HEARTBEAT_TIMEOUT must be at least 10s"},
		{"heartbeat timeout at two heartbeats leaves no room for presence", map[string]string{"WS_HEARTBEAT_TIMEOUT": "10s"}, "WS_PRESENCE_TTL 15s must not exceed WS_HEARTBEAT_TIMEOUT + WS_PRESENCE_STAMP_EVERY = 12s"},
		{"heartbeat timeout with room for presence", map[string]string{"WS_HEARTBEAT_TIMEOUT": "13s"}, ""},
		{"stop phases over the budget", map[string]string{"WS_SHUTDOWN_BUDGET": "9999ms"}, "WS_DRAIN_DELAY + CORE_REQUEST_DEADLINE + 2s (feed) + 1s (close) = 10s must not exceed WS_SHUTDOWN_BUDGET 9.999s"},
		{"stop phases at the budget", map[string]string{"WS_SHUTDOWN_BUDGET": "10s"}, ""},
		{"request deadline counts in the budget", map[string]string{"CORE_REQUEST_DEADLINE": "12001ms"}, "WS_SHUTDOWN_BUDGET"},
		{"drain delay counts in the budget", map[string]string{"WS_DRAIN_DELAY": "7001ms"}, "WS_SHUTDOWN_BUDGET"},
		{"missing nats url", map[string]string{"NATS_URL": ""}, "NATS_URL is required"},
		{"bad event stream", map[string]string{"EVT_STREAM": "chatim.evt"}, "EVT_STREAM must be"},
		{"bad publish stream", map[string]string{"WS_PUBLISH_STREAM": "ws"}, "WS_PUBLISH_STREAM must be"},
		{"same streams", map[string]string{"WS_PUBLISH_STREAM": "CHATIM_EVT"}, "WS_PUBLISH_STREAM must differ from EVT_STREAM"},
		{"bad event root", map[string]string{"EVT_SUBJECT_ROOT": "evt.>"}, "EVT_SUBJECT_ROOT must be"},
		{"bad publish root", map[string]string{"WS_PUBLISH_ROOT": "Web"}, "WS_PUBLISH_ROOT must be"},
		{"overlapping roots", map[string]string{"WS_PUBLISH_ROOT": "evt.publish"}, "WS_PUBLISH_ROOT must not start"},
		{"zero replicas", map[string]string{"EVT_STREAM_REPLICAS": "0"}, "EVT_STREAM_REPLICAS must be positive"},
		{"zero workers", map[string]string{"WS_FANOUT_WORKERS": "0"}, "WS_FANOUT_WORKERS must be positive"},
		{"zero fanout queue", map[string]string{"WS_FANOUT_QUEUE": "0"}, "WS_FANOUT_QUEUE must be positive"},
		{"zero replay rate", map[string]string{"WS_REPLAY_RATE": "0"}, "WS_REPLAY_RATE must be positive"},
		{"zero consume batch", map[string]string{"WS_CONSUME_BATCH": "0"}, "WS_CONSUME_BATCH must be positive"},
		{"consume bytes under 1MB", map[string]string{"WS_CONSUME_BYTES": "1048575"}, "WS_CONSUME_BYTES must be at least 1048576"},
		{"consume bytes at 1MB", map[string]string{"WS_CONSUME_BYTES": "1048576"}, ""},
		{"zero room ids inflight", map[string]string{"WS_ROOM_IDS_INFLIGHT": "0"}, "WS_ROOM_IDS_INFLIGHT must be positive"},
		{"zero open pending", map[string]string{"WS_OPEN_PENDING": "0s"}, "WS_OPEN_PENDING must be positive"},
		{"zero reload rate", map[string]string{"WS_RELOAD_RATE": "0"}, "WS_RELOAD_RATE must be positive"},
		{"reload rate of one per nanosecond", map[string]string{"WS_RELOAD_RATE": "1000000000"}, ""},
		{"reload rate above one per nanosecond", map[string]string{"WS_RELOAD_RATE": "1000000001"}, "WS_RELOAD_RATE must be at most one reload per nanosecond"},
		{"replay max age at the catch-up age", map[string]string{"WS_REPLAY_MAX_AGE": "5s"}, "WS_REPLAY_MAX_AGE must exceed the 5s catch-up age"},
		{"replay max age above the catch-up age", map[string]string{"WS_REPLAY_MAX_AGE": "6s"}, ""},
		{"zero typing rate", map[string]string{"WS_TYPING_RATE": "0"}, "WS_TYPING_RATE must be positive"},
		{"zero typing burst", map[string]string{"WS_TYPING_BURST": "0"}, "WS_TYPING_BURST must be positive"},
		{"negative typing queue", map[string]string{"WS_TYPING_QUEUE": "-1"}, "WS_TYPING_QUEUE must be positive"},
		{"zero typing pending messages", map[string]string{"WS_TYPING_PENDING_MSGS": "0"}, "WS_TYPING_PENDING_MSGS must be positive"},
		{"unparsable typing rate", map[string]string{"WS_TYPING_RATE": "fast"}, "WS_TYPING_RATE"},
		{"one typing frame per second", map[string]string{"WS_TYPING_RATE": "1", "WS_TYPING_BURST": "1"}, ""},
		{"blank presence redis addr uses the default", map[string]string{"WS_PRESENCE_REDIS_ADDR": ""}, ""},
		{"negative presence redis db", map[string]string{"WS_PRESENCE_REDIS_DB": "-1"}, "WS_PRESENCE_REDIS_DB must not be negative"},
		{"zero presence redis timeout", map[string]string{"WS_PRESENCE_REDIS_TIMEOUT": "0s"}, "WS_PRESENCE_REDIS_TIMEOUT must be positive"},
		{"zero presence redis pool", map[string]string{"WS_PRESENCE_REDIS_POOL": "0"}, "WS_PRESENCE_REDIS_POOL must be positive"},
		{"zero presence ttl", map[string]string{"WS_PRESENCE_TTL": "0s"}, "WS_PRESENCE_TTL must be positive"},
		{"zero presence stamp interval", map[string]string{"WS_PRESENCE_STAMP_EVERY": "0s"}, "WS_PRESENCE_STAMP_EVERY must be positive"},
		{"zero presence flush", map[string]string{"WS_PRESENCE_FLUSH": "0s"}, "WS_PRESENCE_FLUSH must be positive"},
		{"zero presence batch", map[string]string{"WS_PRESENCE_BATCH": "0"}, "WS_PRESENCE_BATCH must be positive"},
		{"presence batch at 1000", map[string]string{"WS_PRESENCE_BATCH": "1000"}, ""},
		{"presence batch over 1000", map[string]string{"WS_PRESENCE_BATCH": "1001"}, "WS_PRESENCE_BATCH must be at most 1000"},
		{"unparsable presence ttl", map[string]string{"WS_PRESENCE_TTL": "long"}, "WS_PRESENCE_TTL"},
		{"presence refresh at the ttl", map[string]string{"WS_PRESENCE_TTL": "14500ms"}, "WS_PRESENCE_STAMP_EVERY + 10s (frame gap) + 2 x WS_PRESENCE_FLUSH + WS_PRESENCE_REDIS_TIMEOUT = 14.5s must be below WS_PRESENCE_TTL 14.5s"},
		{"presence refresh just below the ttl", map[string]string{"WS_PRESENCE_TTL": "14501ms"}, ""},
		{"presence redis timeout counts in the refresh", map[string]string{"WS_PRESENCE_REDIS_TIMEOUT": "1s"}, "must be below WS_PRESENCE_TTL"},
		{"presence flush counts twice in the refresh", map[string]string{"WS_PRESENCE_FLUSH": "1250ms"}, "must be below WS_PRESENCE_TTL"},
		{"presence ttl at heartbeat timeout plus stamp interval", map[string]string{"WS_PRESENCE_TTL": "17s"}, ""},
		{"presence ttl over heartbeat timeout plus stamp interval", map[string]string{"WS_PRESENCE_TTL": "17001ms"}, "WS_PRESENCE_TTL 17.001s must not exceed WS_HEARTBEAT_TIMEOUT + WS_PRESENCE_STAMP_EVERY = 17s"},
		{"zero presence cache", map[string]string{"WS_PRESENCE_CACHE": "0s"}, "WS_PRESENCE_CACHE must be positive"},
		{"presence cache at the ttl", map[string]string{"WS_PRESENCE_CACHE": "15s"}, "WS_PRESENCE_CACHE 15s must be below WS_PRESENCE_TTL 15s"},
		{"presence cache just below the ttl", map[string]string{"WS_PRESENCE_CACHE": "14999ms"}, ""},
		{"unparsable presence cache", map[string]string{"WS_PRESENCE_CACHE": "soon"}, "WS_PRESENCE_CACHE"},
		{"zero presence watch max", map[string]string{"WS_PRESENCE_WATCH_MAX": "0"}, "WS_PRESENCE_WATCH_MAX must be positive"},
		{"one presence watch user", map[string]string{"WS_PRESENCE_WATCH_MAX": "1"}, ""},
		{"presence ttl follows the heartbeat timeout", map[string]string{"WS_HEARTBEAT_TIMEOUT": "12s"}, "must not exceed WS_HEARTBEAT_TIMEOUT + WS_PRESENCE_STAMP_EVERY = 14s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			_, err := config.Load()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Load() = %v, want nil", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("Load() = nil, want an error containing %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("Load() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRequiresThePresenceRedisAddress(t *testing.T) {
	setEnv(t, nil)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.PresenceRedisAddr = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "WS_PRESENCE_REDIS_ADDR is required") {
		t.Fatalf("Validate() = %v, want WS_PRESENCE_REDIS_ADDR is required", err)
	}
}

func TestLoadReportsEveryBrokenRule(t *testing.T) {
	setEnv(t, map[string]string{"WS_JWT_ISSUER": "", "WS_FRAME_MAX": "10", "WS_SEND_QUEUE": "0", "WS_SHUTDOWN_BUDGET": "1s"})
	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil, want errors")
	}
	for _, key := range []string{"WS_JWT_ISSUER", "WS_FRAME_MAX", "WS_SEND_QUEUE", "WS_SHUTDOWN_BUDGET"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("Load() error %q does not mention %s", err, key)
		}
	}
}

func TestLoadNeverEchoesRedisPassword(t *testing.T) {
	setEnv(t, map[string]string{"REDIS_PASSWORD": "st4t3s3cr3t", "WS_FRAME_MAX": "10", "REDIS_DB": "x"})
	_, err := config.Load()
	if err == nil || strings.Contains(err.Error(), "st4t3s3cr3t") {
		t.Fatalf("Load() = %v, want an error that hides the redis password", err)
	}
}

func TestValidateChecksAStructBuiltByHand(t *testing.T) {
	if err := (config.Config{}).Validate(); err == nil {
		t.Fatal("Validate() on a zero Config = nil, want errors")
	}
}
