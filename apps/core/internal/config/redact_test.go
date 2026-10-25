package config_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func TestConfigLogValueHidesSecrets(t *testing.T) {
	setEnv(t, map[string]string{
		"MONGO_URI":             "mongodb://chatim:s3cr3t@chatim-mongodb:27017/?replicaSet=rs0&authSource=admin",
		"NATS_URL":              "nats://core:s3cr3t@chatim-nats:4222",
		"REDIS_PASSWORD":        "st4t3s3cr3t",
		"REDIS_DEDUPE_PASSWORD": "d3dup3s3cr3t",
	})
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)
	logged := buf.String()
	for _, out := range []string{logged, fmt.Sprintf("%v %+v %s", cfg, cfg, cfg)} {
		if strings.Contains(out, "s3cr3t") || strings.Contains(out, "st4t3") || strings.Contains(out, "d3dup3") {
			t.Fatalf("summary leaks a secret: %s", out)
		}
	}
	for _, want := range []string{"xxxxx@chatim-mongodb:27017", "xxxxx@chatim-nats:4222", `"stream":{"name":"CHATIM_EVT"`,
		`"redis_auth":true`, `"redis_dedupe_addr":"chatim-redis-dedupe:6379"`, `"redis_dedupe_db":0`, `"redis_dedupe_auth":true`,
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("summary %s misses %s", logged, want)
		}
	}
}

func TestConfigLogValueReportsRedisWithoutAuth(t *testing.T) {
	setEnv(t, nil)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := cfg.String()
	for _, want := range []string{"redis_auth=false", "redis_dedupe_auth=false"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary %s misses %s", out, want)
		}
	}
}

func TestSecretsListsSetPasswordsLongestFirst(t *testing.T) {
	tests := []struct {
		mongo, state, dedupe string
		want                 []string
	}{
		{"", "", "", nil},
		{"", "short", "", []string{"short"}},
		{"", "", "dedupe-only", []string{"dedupe-only"}},
		{"", "short", "much-longer", []string{"much-longer", "short"}},
		{"mongo-pw-longest", "short", "mid-size", []string{"mongo-pw-longest", "mid-size", "short"}},
	}
	for _, tt := range tests {
		got := config.Config{MongoPassword: tt.mongo, RedisPassword: tt.state, RedisDedupePassword: tt.dedupe}.Secrets()
		if !slices.Equal(got, tt.want) {
			t.Errorf("Secrets(%q, %q, %q) = %q, want %q", tt.mongo, tt.state, tt.dedupe, got, tt.want)
		}
	}
}
