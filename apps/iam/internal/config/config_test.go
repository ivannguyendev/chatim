package config_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/config"
)

var kek = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("IAM_PUBLIC_URL", "https://iam.example.com")
	t.Setenv("IAM_PG_URI", "postgres://iam@chatim-postgres:5432/chatim_iam")
	t.Setenv("IAM_PG_PASSWORD", "pg-secret-value")
	t.Setenv("IAM_ISSUER", "chatim-dev")
	t.Setenv("IAM_AUDIENCE", "chatim-websocket")
	t.Setenv("IAM_KEY_ENCRYPTION_KEY", kek)
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ListenAddr != ":8080" || c.AdminAddr != ":9090" || c.AccessTTL != 15*time.Minute || c.SessionIdle != 720*time.Hour ||
		c.SessionMax != 2160*time.Hour || c.RefreshGrace != 30*time.Second || c.HashConcurrency != 16 || c.LoginMaxDelay != time.Minute ||
		c.LoginIPRate != 20 || c.LoginIPBurst != 200 || c.PasswordMin != 12 || c.BodyMax != 16<<10 || c.DPoPWindow != time.Minute {
		t.Fatalf("defaults = %+v", c)
	}
	if len(c.KeyEncryptionKey) != 32 {
		t.Fatalf("kek length = %d, want 32", len(c.KeyEncryptionKey))
	}
	if uri := c.DatabaseURI(); !strings.Contains(uri, "pg-secret-value") {
		t.Fatal("DatabaseURI does not carry the password")
	}
}

func TestLogValueHidesSecrets(t *testing.T) {
	setRequired(t)
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := c.String()
	for _, secret := range []string{"pg-secret-value", kek} {
		if strings.Contains(out, secret) {
			t.Fatalf("config log contains a secret: %s", out)
		}
	}
}

func TestSecretsReadFromFiles(t *testing.T) {
	setRequired(t)
	dir := t.TempDir()
	pw := filepath.Join(dir, "pw")
	key := filepath.Join(dir, "kek")
	_ = os.WriteFile(pw, []byte("from-file\n"), 0o600)
	_ = os.WriteFile(key, []byte(kek+"\n"), 0o600)
	t.Setenv("IAM_PG_PASSWORD", "")
	t.Setenv("IAM_PG_PASSWORD_FILE", pw)
	t.Setenv("IAM_KEY_ENCRYPTION_KEY", "")
	t.Setenv("IAM_KEY_ENCRYPTION_KEY_FILE", key)
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(c.DatabaseURI(), "from-file") || len(c.KeyEncryptionKey) != 32 {
		t.Fatal("secrets were not read from files")
	}
}

func TestLoadRejectsBadConfig(t *testing.T) {
	cases := map[string]map[string]string{
		"missing public url":    {"IAM_PUBLIC_URL": ""},
		"public url with query": {"IAM_PUBLIC_URL": "https://iam.example.com?x=1"},
		"missing issuer":        {"IAM_ISSUER": ""},
		"missing kek":           {"IAM_KEY_ENCRYPTION_KEY": ""},
		"short kek":             {"IAM_KEY_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString([]byte("short"))},
		"kek not base64":        {"IAM_KEY_ENCRYPTION_KEY": "!!!"},
		"pg uri with password":  {"IAM_PG_URI": "postgres://iam:inline@host/db"},
		"grace over access ttl": {"IAM_REFRESH_GRACE": "20m"},
		"idle over max":         {"IAM_SESSION_IDLE": "3000h"},
		"same ports":            {"IAM_ADMIN_ADDR": ":8080"},
		"bad duration":          {"IAM_ACCESS_TTL": "soon"},
		"zero hash slots":       {"IAM_HASH_CONCURRENCY": "0"},
		"short password min":    {"IAM_PASSWORD_MIN": "6"},
		"budget too small":      {"IAM_SHUTDOWN_BUDGET": "1s"},
		"bad cors origin":       {"IAM_CORS_ORIGINS": "app.example.com"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := config.Load(); err == nil {
				t.Fatal("Load accepted a bad config")
			}
		})
	}
}
