package config_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/config"
)

var envKeys = []string{
	"GW_ADDR", "GW_ADMIN_ADDR", "GW_CORE_ADDRS", "GW_JWT_KEYS_FILE", "GW_JWT_ISSUER", "GW_JWT_AUDIENCE", "GW_JWT_LEEWAY",
	"GW_BODY_MAX", "GW_USER_RATE", "GW_USER_BURST", "GW_USER_INFLIGHT", "GW_USER_LIMITERS", "GW_INFLIGHT_MAX",
	"GW_CORS_ORIGINS", "GW_DRAIN_DELAY", "GW_SHUTDOWN_BUDGET", "GW_READ_HEADER_TIMEOUT", "GW_READ_TIMEOUT",
	"GW_WRITE_TIMEOUT", "GW_IDLE_TIMEOUT", "GW_MAX_HEADER_BYTES", "GW_H2_MAX_STREAMS", "CORE_REQUEST_DEADLINE",
}

var requiredEnv = map[string]string{
	"GW_CORE_ADDRS":    "chatim-core-1:9000,chatim-core-2:9000",
	"GW_JWT_KEYS_FILE": "/run/secrets/jwks.json",
	"GW_JWT_ISSUER":    "chatim-identity",
	"GW_JWT_AUDIENCE":  "chatim-gateway",
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

func mustLoad(t *testing.T) config.Config {
	t.Helper()
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, nil)
	got := mustLoad(t)
	want := config.Config{
		ListenAddr:        ":8080",
		AdminAddr:         ":9090",
		CoreAddrs:         []string{"chatim-core-1:9000", "chatim-core-2:9000"},
		JWTKeysFile:       "/run/secrets/jwks.json",
		JWTIssuer:         "chatim-identity",
		JWTAudience:       "chatim-gateway",
		JWTLeeway:         30 * time.Second,
		BodyMax:           128 << 10,
		UserRate:          50,
		UserBurst:         100,
		UserInflight:      32,
		UserLimiters:      100000,
		InflightMax:       10000,
		DrainDelay:        2 * time.Second,
		ShutdownBudget:    15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		H2MaxStreams:      250,
		RequestDeadline:   5 * time.Second,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %+v\nwant %+v", got, want)
	}
}

func TestLoadReadsOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"GW_ADDR": "127.0.0.1:7080", "GW_ADMIN_ADDR": "127.0.0.1:7090", "GW_CORE_ADDRS": " a:1 , ,b:2 ",
		"GW_JWT_LEEWAY": "0s", "GW_BODY_MAX": "4096", "GW_USER_RATE": "5", "GW_USER_BURST": "6", "GW_USER_INFLIGHT": "7",
		"GW_USER_LIMITERS": "900", "GW_INFLIGHT_MAX": "800", "GW_CORS_ORIGINS": "https://chat.example.com, http://localhost:3000",
		"GW_DRAIN_DELAY": "1s", "GW_SHUTDOWN_BUDGET": "20s", "GW_READ_HEADER_TIMEOUT": "2s", "GW_READ_TIMEOUT": "3s",
		"GW_IDLE_TIMEOUT": "30s", "GW_MAX_HEADER_BYTES": "8192", "GW_H2_MAX_STREAMS": "100", "CORE_REQUEST_DEADLINE": "8s",
	})
	got := mustLoad(t)
	if got.ListenAddr != "127.0.0.1:7080" || got.AdminAddr != "127.0.0.1:7090" || !reflect.DeepEqual(got.CoreAddrs, []string{"a:1", "b:2"}) ||
		got.JWTLeeway != 0 || got.BodyMax != 4096 || got.UserRate != 5 || got.UserBurst != 6 || got.UserInflight != 7 ||
		got.UserLimiters != 900 || got.InflightMax != 800 || !reflect.DeepEqual(got.CORSOrigins, []string{"https://chat.example.com", "http://localhost:3000"}) ||
		got.DrainDelay != time.Second || got.ShutdownBudget != 20*time.Second || got.ReadHeaderTimeout != 2*time.Second || got.ReadTimeout != 3*time.Second ||
		got.IdleTimeout != 30*time.Second || got.MaxHeaderBytes != 8192 || got.H2MaxStreams != 100 || got.RequestDeadline != 8*time.Second {
		t.Fatalf("Load() = %+v", got)
	}
	if got.WriteTimeout != 13*time.Second {
		t.Fatalf("WriteTimeout = %v, want CORE_REQUEST_DEADLINE + 5s", got.WriteTimeout)
	}
}

func TestLogValueCoversEveryField(t *testing.T) {
	setEnv(t, map[string]string{"GW_CORS_ORIGINS": "https://chat.example.com"})
	cfg := mustLoad(t)
	attrs := cfg.LogValue().Group()
	if got, want := len(attrs), reflect.TypeFor[config.Config]().NumField(); got != want {
		t.Fatalf("LogValue has %d attrs, Config has %d fields", got, want)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)
	for _, want := range []string{`"core_addrs":"chatim-core-1:9000,chatim-core-2:9000"`, `"cors_origins":"https://chat.example.com"`, `"body_max":131072`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("summary %s misses %s", buf.String(), want)
		}
	}
	if s := fmt.Sprint(cfg); !strings.Contains(s, "user_limiters=100000") {
		t.Fatalf("String() = %s", s)
	}
}

func TestAdminAddrReadsOnlyItsVariable(t *testing.T) {
	setEnv(t, map[string]string{"GW_ADMIN_ADDR": "127.0.0.1:7090", "GW_CORE_ADDRS": ""})
	if got := config.AdminAddr(); got != "127.0.0.1:7090" {
		t.Fatalf("AdminAddr = %q", got)
	}
	t.Setenv("GW_ADMIN_ADDR", "")
	if got := config.AdminAddr(); got != ":9090" {
		t.Fatalf("default AdminAddr = %q", got)
	}
}
