package config_test

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/config"
)

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"defaults are valid", nil, ""},
		{"negative redis db", map[string]string{"REDIS_DB": "-1"}, "REDIS_DB must not be negative"},
		{"unreadable redis password file", map[string]string{"REDIS_PASSWORD_FILE": "/no/such/file"}, "REDIS_PASSWORD_FILE"},
		{"missing keys file", map[string]string{"JWT_KEYS_FILE": ""}, "JWT_KEYS_FILE is required"},
		{"missing issuer", map[string]string{"JWT_ISSUER": ""}, "JWT_ISSUER is required"},
		{"missing audience", map[string]string{"JWT_AUDIENCE": ""}, "JWT_AUDIENCE is required"},
		{"leeway over a minute", map[string]string{"JWT_LEEWAY": "2m"}, "JWT_LEEWAY must not exceed 1m0s"},
		{"identity without secret", map[string]string{"IDENTITY_NODE_SECRET": ""}, "node secret is required"},
		{"identity without internal url", map[string]string{"IDENTITY_INTERNAL_URL": ""}, "internal url is required"},
		{"identity without jwks url", map[string]string{"IDENTITY_JWKS_URL": ""}, "jwks url is required"},
		{"identity keys interval over a minute", map[string]string{"IDENTITY_KEYS_INTERVAL": "61s"}, "keys interval must not exceed"},
		{"identity jwks over http", map[string]string{"IDENTITY_JWKS_URL": "http://chatim-identity-1:8080/.well-known/jwks.json"}, "jwks url must be an absolute https url"},
		{"negative shared redis db", map[string]string{"SHARED_REDIS_DB": "-1"}, "SHARED_REDIS_DB must not be negative"},
		{"negative leeway", map[string]string{"JWT_LEEWAY": "-1s"}, "JWT_LEEWAY must not be negative"},
		{"same addresses", map[string]string{"APP_ADDR": ":9090"}, "APP_ADMIN_ADDR must differ from APP_ADDR"},
		{"unparsable count", map[string]string{"APP_USER_RATE": "many"}, "APP_USER_RATE"},
		{"unparsable duration", map[string]string{"APP_DRAIN_DELAY": "soon"}, "APP_DRAIN_DELAY"},
		{"zero body max", map[string]string{"APP_BODY_SIZE_MAX": "0"}, "APP_BODY_SIZE_MAX must be positive"},
		{"zero user rate", map[string]string{"APP_USER_RATE": "0"}, "APP_USER_RATE must be positive"},
		{"zero user burst", map[string]string{"APP_USER_BURST": "0"}, "APP_USER_BURST must be positive"},
		{"zero user inflight", map[string]string{"APP_USER_INFLIGHT": "0"}, "APP_USER_INFLIGHT must be positive"},
		{"zero user limiters", map[string]string{"APP_USER_LIMITERS": "0"}, "APP_USER_LIMITERS must be positive"},
		{"zero inflight max", map[string]string{"APP_INFLIGHT_MAX": "0"}, "APP_INFLIGHT_MAX must be positive"},
		{"limiters under the node cap", map[string]string{"APP_USER_LIMITERS": "9999"}, "APP_USER_LIMITERS 9999 must be at least APP_INFLIGHT_MAX 10000"},
		{"limiters at the node cap", map[string]string{"APP_USER_LIMITERS": "10000"}, ""},
		{"zero drain delay", map[string]string{"APP_DRAIN_DELAY": "0s"}, "APP_DRAIN_DELAY must be positive"},
		{"zero request deadline", map[string]string{"CHAT_REQUEST_DEADLINE": "0s"}, "CHAT_REQUEST_DEADLINE must be positive"},
		{"budget at drain + deadline", map[string]string{"APP_SHUTDOWN_BUDGET": "7s"}, "APP_SHUTDOWN_BUDGET 7s must exceed APP_DRAIN_DELAY + CHAT_REQUEST_DEADLINE = 7s"},
		{"budget over drain + deadline", map[string]string{"APP_SHUTDOWN_BUDGET": "7001ms"}, ""},
		{"zero read header timeout", map[string]string{"APP_READ_HEADER_TIMEOUT": "0s"}, "APP_READ_HEADER_TIMEOUT must be positive"},
		{"read header over read", map[string]string{"APP_READ_HEADER_TIMEOUT": "11s"}, "APP_READ_HEADER_TIMEOUT must not exceed APP_READ_TIMEOUT"},
		{"zero read timeout", map[string]string{"APP_READ_TIMEOUT": "0s"}, "APP_READ_TIMEOUT must be positive"},
		{"zero idle timeout", map[string]string{"APP_IDLE_TIMEOUT": "0s"}, "APP_IDLE_TIMEOUT must be positive"},
		{"zero header bytes", map[string]string{"APP_MAX_HEADER_BYTES": "0"}, "APP_MAX_HEADER_BYTES must be positive"},
		{"zero h2 streams", map[string]string{"GW_H2_MAX_STREAMS": "0"}, "GW_H2_MAX_STREAMS must be positive"},
		{"write timeout under deadline + 5s", map[string]string{"APP_WRITE_TIMEOUT": "9999ms"}, "APP_WRITE_TIMEOUT 9.999s must be at least CHAT_REQUEST_DEADLINE + 5s = 10s"},
		{"write timeout at deadline + 5s", map[string]string{"APP_WRITE_TIMEOUT": "10s"}, ""},
		{"origin", map[string]string{"APP_CORS_ORIGINS": "https://chat.example.com:8443"}, ""},
		{"wildcard origin", map[string]string{"APP_CORS_ORIGINS": "*"}, `APP_CORS_ORIGINS entry "*" must be an exact origin`},
		{"origin with path", map[string]string{"APP_CORS_ORIGINS": "https://chat.example.com/"}, "must be an exact origin"},
		{"origin without scheme", map[string]string{"APP_CORS_ORIGINS": "chat.example.com"}, "must be an exact origin"},
		{"origin with user", map[string]string{"APP_CORS_ORIGINS": "https://u@chat.example.com"}, "must be an exact origin"},
		{"origin upper case", map[string]string{"APP_CORS_ORIGINS": "HTTPS://chat.example.com"}, "must be an exact origin"},
		{"ftp origin", map[string]string{"APP_CORS_ORIGINS": "ftp://chat.example.com"}, "must be an exact origin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			_, err := config.Load()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Load() = %v, want no error", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Load() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}
