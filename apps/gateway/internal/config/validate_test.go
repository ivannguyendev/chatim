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
		{"missing core addrs", map[string]string{"GW_CORE_ADDRS": " , "}, "GW_CORE_ADDRS is required"},
		{"core addr without port", map[string]string{"GW_CORE_ADDRS": "chatim-core-1"}, `GW_CORE_ADDRS entry "chatim-core-1" must be host:port`},
		{"core addr without host", map[string]string{"GW_CORE_ADDRS": ":9000"}, "must be host:port"},
		{"core addr twice", map[string]string{"GW_CORE_ADDRS": "a:1,a:1"}, `GW_CORE_ADDRS lists "a:1" twice`},
		{"missing keys file", map[string]string{"GW_JWT_KEYS_FILE": ""}, "GW_JWT_KEYS_FILE is required"},
		{"missing issuer", map[string]string{"GW_JWT_ISSUER": ""}, "GW_JWT_ISSUER is required"},
		{"missing audience", map[string]string{"GW_JWT_AUDIENCE": ""}, "GW_JWT_AUDIENCE is required"},
		{"negative leeway", map[string]string{"GW_JWT_LEEWAY": "-1s"}, "GW_JWT_LEEWAY must not be negative"},
		{"same addresses", map[string]string{"GW_ADDR": ":9090"}, "GW_ADMIN_ADDR must differ from GW_ADDR"},
		{"unparsable count", map[string]string{"GW_USER_RATE": "many"}, "GW_USER_RATE"},
		{"unparsable duration", map[string]string{"GW_DRAIN_DELAY": "soon"}, "GW_DRAIN_DELAY"},
		{"zero body max", map[string]string{"GW_BODY_MAX": "0"}, "GW_BODY_MAX must be positive"},
		{"zero user rate", map[string]string{"GW_USER_RATE": "0"}, "GW_USER_RATE must be positive"},
		{"zero user burst", map[string]string{"GW_USER_BURST": "0"}, "GW_USER_BURST must be positive"},
		{"zero user inflight", map[string]string{"GW_USER_INFLIGHT": "0"}, "GW_USER_INFLIGHT must be positive"},
		{"zero user limiters", map[string]string{"GW_USER_LIMITERS": "0"}, "GW_USER_LIMITERS must be positive"},
		{"zero inflight max", map[string]string{"GW_INFLIGHT_MAX": "0"}, "GW_INFLIGHT_MAX must be positive"},
		{"limiters under the node cap", map[string]string{"GW_USER_LIMITERS": "9999"}, "GW_USER_LIMITERS 9999 must be at least GW_INFLIGHT_MAX 10000"},
		{"limiters at the node cap", map[string]string{"GW_USER_LIMITERS": "10000"}, ""},
		{"zero drain delay", map[string]string{"GW_DRAIN_DELAY": "0s"}, "GW_DRAIN_DELAY must be positive"},
		{"zero request deadline", map[string]string{"CORE_REQUEST_DEADLINE": "0s"}, "CORE_REQUEST_DEADLINE must be positive"},
		{"budget at drain + deadline", map[string]string{"GW_SHUTDOWN_BUDGET": "7s"}, "GW_SHUTDOWN_BUDGET 7s must exceed GW_DRAIN_DELAY + CORE_REQUEST_DEADLINE = 7s"},
		{"budget over drain + deadline", map[string]string{"GW_SHUTDOWN_BUDGET": "7001ms"}, ""},
		{"zero read header timeout", map[string]string{"GW_READ_HEADER_TIMEOUT": "0s"}, "GW_READ_HEADER_TIMEOUT must be positive"},
		{"read header over read", map[string]string{"GW_READ_HEADER_TIMEOUT": "11s"}, "GW_READ_HEADER_TIMEOUT must not exceed GW_READ_TIMEOUT"},
		{"zero read timeout", map[string]string{"GW_READ_TIMEOUT": "0s"}, "GW_READ_TIMEOUT must be positive"},
		{"zero idle timeout", map[string]string{"GW_IDLE_TIMEOUT": "0s"}, "GW_IDLE_TIMEOUT must be positive"},
		{"zero header bytes", map[string]string{"GW_MAX_HEADER_BYTES": "0"}, "GW_MAX_HEADER_BYTES must be positive"},
		{"zero h2 streams", map[string]string{"GW_H2_MAX_STREAMS": "0"}, "GW_H2_MAX_STREAMS must be positive"},
		{"write timeout under deadline + 5s", map[string]string{"GW_WRITE_TIMEOUT": "9999ms"}, "GW_WRITE_TIMEOUT 9.999s must be at least CORE_REQUEST_DEADLINE + 5s = 10s"},
		{"write timeout at deadline + 5s", map[string]string{"GW_WRITE_TIMEOUT": "10s"}, ""},
		{"origin", map[string]string{"GW_CORS_ORIGINS": "https://chat.example.com:8443"}, ""},
		{"wildcard origin", map[string]string{"GW_CORS_ORIGINS": "*"}, `GW_CORS_ORIGINS entry "*" must be an exact origin`},
		{"origin with path", map[string]string{"GW_CORS_ORIGINS": "https://chat.example.com/"}, "must be an exact origin"},
		{"origin without scheme", map[string]string{"GW_CORS_ORIGINS": "chat.example.com"}, "must be an exact origin"},
		{"origin with user", map[string]string{"GW_CORS_ORIGINS": "https://u@chat.example.com"}, "must be an exact origin"},
		{"origin upper case", map[string]string{"GW_CORS_ORIGINS": "HTTPS://chat.example.com"}, "must be an exact origin"},
		{"ftp origin", map[string]string{"GW_CORS_ORIGINS": "ftp://chat.example.com"}, "must be an exact origin"},
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
