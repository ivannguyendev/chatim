package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func secretFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	return path
}

func TestLoadReadsPasswordFiles(t *testing.T) {
	tests := []struct {
		name, content, plain, want string
	}{
		{"trailing newline trimmed", "file-pw\n", "", "file-pw"},
		{"crlf trimmed", "file-pw\r\n", "", "file-pw"},
		{"no newline", "file-pw", "", "file-pw"},
		{"file wins over the plain var", "file-pw\n", "plain-pw", "file-pw"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, map[string]string{
				"MONGO_USER": "core", "MONGO_PASSWORD_FILE": secretFile(t, "mongo-"+tt.content), "MONGO_PASSWORD": tt.plain,
				"REDIS_PASSWORD_FILE": secretFile(t, tt.content), "REDIS_PASSWORD": tt.plain,
				"REDIS_DEDUPE_PASSWORD_FILE": secretFile(t, "dedupe-"+tt.content), "REDIS_DEDUPE_PASSWORD": tt.plain,
			})
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.MongoPassword != "mongo-"+tt.want {
				t.Errorf("MongoPassword = %q, want %q", cfg.MongoPassword, "mongo-"+tt.want)
			}
			if cfg.RedisPassword != tt.want || cfg.RedisDedupePassword != "dedupe-"+tt.want {
				t.Errorf("passwords = %q, %q; want %q, %q", cfg.RedisPassword, cfg.RedisDedupePassword, tt.want, "dedupe-"+tt.want)
			}
		})
	}
}

func TestLoadUsesThePlainPasswordWithoutAFile(t *testing.T) {
	setEnv(t, map[string]string{"REDIS_PASSWORD": "plain-pw", "REDIS_DEDUPE_PASSWORD": "plain-dedupe-pw"})
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RedisPassword != "plain-pw" || cfg.RedisDedupePassword != "plain-dedupe-pw" {
		t.Errorf("passwords = %q, %q", cfg.RedisPassword, cfg.RedisDedupePassword)
	}
}

func TestLoadRejectsUnreadablePasswordFiles(t *testing.T) {
	dir := t.TempDir()
	for _, key := range secretFileKeys {
		t.Run(key, func(t *testing.T) {
			setEnv(t, map[string]string{key: filepath.Join(dir, "missing-s3cr3t-name"), "REDIS_PASSWORD": "plain-s3cr3t"})
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("Load() = %v, want an error naming %s", err, key)
			}
			if strings.Contains(err.Error(), "plain-s3cr3t") {
				t.Errorf("error leaks a password: %v", err)
			}
		})
	}
}

func TestLoadErrorNeverShowsPasswordFileContent(t *testing.T) {
	setEnv(t, map[string]string{"REDIS_PASSWORD_FILE": secretFile(t, "content-s3cr3t\n"), "REDIS_DB": "x"})
	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil, want the REDIS_DB parse error")
	}
	if strings.Contains(err.Error(), "content-s3cr3t") {
		t.Errorf("error leaks the file content: %v", err)
	}
}
