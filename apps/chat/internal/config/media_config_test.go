package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/config"
)

func TestMediaConfigIsReadFromTheEnvironment(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "s3")
	if err := os.WriteFile(secret, []byte("top-secret-s3-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, map[string]string{
		"MEDIA_S3_ENDPOINT": "http://chatim-s3:3900", "MEDIA_S3_PUBLIC_ENDPOINT": "https://media.example.com",
		"MEDIA_S3_REGION": "us-east-1", "MEDIA_S3_ACCESS_KEY": "GK1", "MEDIA_S3_SECRET_KEY_FILE": secret,
		"MEDIA_S3_BUCKET_PROCESSING": "p", "MEDIA_S3_BUCKET_COMPLETED": "c",
		"MEDIA_MAX_PARALLEL_UPLOADS": "6", "MEDIA_PART_URL_TTL": "10m",
	})
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m := c.Media
	if !m.Enabled || m.SecretKey != "top-secret-s3-key" || m.MaxParallelUploads != 6 || m.PartSignatureTTL != 10*time.Minute || m.MaxPartBytes != 128<<20 {
		t.Fatalf("media %+v", m)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if logged := c.String(); strings.Contains(logged, "top-secret-s3-key") || strings.Contains(logged, "GK1") || !strings.Contains(logged, "secret_key=true") || !strings.Contains(logged, "access_key=true") {
		t.Fatalf("log line leaks or misses the secret flag: %s", logged)
	}
	if !slices.Contains(c.Secrets(), "top-secret-s3-key") {
		t.Fatal("media secret not in the redaction list")
	}
}

func TestBrokenMediaConfigDoesNotStopChat(t *testing.T) {
	setEnv(t, map[string]string{"MEDIA_S3_ENDPOINT": "", "MEDIA_MIN_PART_BYTES": "1024"})
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load refused a broken media config: %v", err)
	}
	if c.Media.Validate() == nil {
		t.Fatal("media config should be invalid")
	}
}

func TestMediaNumbersMustParse(t *testing.T) {
	setEnv(t, map[string]string{"MEDIA_MAX_PARTS": "many"})
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "MEDIA_MAX_PARTS") {
		t.Fatalf("err = %v", err)
	}
}
