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
		{"unparsable duration", map[string]string{"WS_AUTH_TIMEOUT": "soon"}, "WS_AUTH_TIMEOUT"},
		{"negative redis db", map[string]string{"REDIS_DB": "-1"}, "REDIS_DB must not be negative"},
		{"zero leeway", map[string]string{"WS_JWT_LEEWAY": "0s"}, ""},
		{"negative leeway", map[string]string{"WS_JWT_LEEWAY": "-1s"}, "WS_JWT_LEEWAY must not be negative"},
		{"zero auth timeout", map[string]string{"WS_AUTH_TIMEOUT": "0s"}, "WS_AUTH_TIMEOUT must be positive"},
		{"zero unauth max", map[string]string{"WS_UNAUTH_MAX": "0"}, "WS_UNAUTH_MAX must be positive"},
		{"negative frame max", map[string]string{"WS_FRAME_MAX": "-1"}, "WS_FRAME_MAX must be positive"},
		{"zero command rate", map[string]string{"WS_COMMAND_RATE": "0"}, "WS_COMMAND_RATE must be positive"},
		{"zero command burst", map[string]string{"WS_COMMAND_BURST": "0"}, "WS_COMMAND_BURST must be positive"},
		{"zero command inflight", map[string]string{"WS_COMMAND_INFLIGHT": "0"}, "WS_COMMAND_INFLIGHT must be positive"},
		{"zero send queue", map[string]string{"WS_SEND_QUEUE": "0"}, "WS_SEND_QUEUE must be positive"},
		{"zero write timeout", map[string]string{"WS_WRITE_TIMEOUT": "0s"}, "WS_WRITE_TIMEOUT must be positive"},
		{"zero request deadline", map[string]string{"CORE_REQUEST_DEADLINE": "0s"}, "CORE_REQUEST_DEADLINE must be positive"},
		{"zero drain delay", map[string]string{"WS_DRAIN_DELAY": "0s"}, "WS_DRAIN_DELAY must be positive"},
		{"zero shutdown budget", map[string]string{"WS_SHUTDOWN_BUDGET": "0s"}, "WS_SHUTDOWN_BUDGET must be positive"},
		{"frame max under 4KB", map[string]string{"WS_FRAME_MAX": "4095"}, "WS_FRAME_MAX must be at least 4096"},
		{"frame max at 4KB", map[string]string{"WS_FRAME_MAX": "4096"}, ""},
		{"heartbeat timeout under two heartbeats", map[string]string{"WS_HEARTBEAT_TIMEOUT": "9999ms"}, "WS_HEARTBEAT_TIMEOUT must be at least 10s"},
		{"heartbeat timeout at two heartbeats", map[string]string{"WS_HEARTBEAT_TIMEOUT": "10s"}, ""},
		{"stop phases over the budget", map[string]string{"WS_SHUTDOWN_BUDGET": "7999ms"}, "WS_DRAIN_DELAY + CORE_REQUEST_DEADLINE + 1s (close) = 8s must not exceed WS_SHUTDOWN_BUDGET 7.999s"},
		{"stop phases at the budget", map[string]string{"WS_SHUTDOWN_BUDGET": "8s"}, ""},
		{"request deadline counts in the budget", map[string]string{"CORE_REQUEST_DEADLINE": "12001ms"}, "WS_SHUTDOWN_BUDGET"},
		{"drain delay counts in the budget", map[string]string{"WS_DRAIN_DELAY": "9001ms"}, "WS_SHUTDOWN_BUDGET"},
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
