package iamsync_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/iamsync"
)

func TestFromEnvDefaultsNeedTheIamURLs(t *testing.T) {
	c, err := iamsync.FromEnv("WS")
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.InternalURL != "" || c.JWKSURL != "" || c.RevocationsEvery != iamsync.DefaultRevocationsEvery || c.KeysEvery != iamsync.DefaultKeysEvery ||
		c.ReadyTimeout != iamsync.DefaultReadyTimeout || c.HTTPTimeout != iamsync.DefaultHTTPTimeout || c.BodyMax != iamsync.DefaultBodyMax ||
		c.SharedTTL != iamsync.DefaultSharedTTL || c.KeysMinGap != iamsync.DefaultKeysMinGap || c.AllowHTTP {
		t.Fatalf("defaults = %+v", c)
	}
	err = c.Validate()
	if err == nil || !strings.Contains(err.Error(), "internal url is required") || !strings.Contains(err.Error(), "jwks url is required") {
		t.Fatalf("Validate without urls = %v", err)
	}
}

func TestFromEnvReadsEveryVariableAndHidesTheSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GW_IAM_INTERNAL_URL", internalURL)
	t.Setenv("GW_IAM_JWKS_URL", "http://iam:8080/.well-known/jwks.json")
	t.Setenv("GW_IAM_SECRET_FILE", path)
	t.Setenv("GW_IAM_REVOCATIONS_INTERVAL", "3s")
	t.Setenv("GW_IAM_KEYS_INTERVAL", "10s")
	t.Setenv("GW_IAM_READY_TIMEOUT", "4s")
	t.Setenv("GW_IAM_ALLOW_HTTP", "true")
	c, err := iamsync.FromEnv("GW")
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.Secret != secret || c.RevocationsEvery != 3*time.Second || c.KeysEvery != 10*time.Second || c.ReadyTimeout != 4*time.Second || !c.AllowHTTP || c.InternalURL != internalURL {
		t.Fatalf("config = %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if out := c.LogValue().String(); strings.Contains(out, secret) || !strings.Contains(out, "secret_set=true") {
		t.Fatalf("log value = %s", out)
	}
}

func TestFromEnvReportsBadValues(t *testing.T) {
	t.Setenv("WS_IAM_REVOCATIONS_INTERVAL", "soon")
	t.Setenv("WS_IAM_ALLOW_HTTP", "maybe")
	if _, err := iamsync.FromEnv("WS"); err == nil || !strings.Contains(err.Error(), "WS_IAM_REVOCATIONS_INTERVAL") || !strings.Contains(err.Error(), "WS_IAM_ALLOW_HTTP") {
		t.Fatalf("FromEnv err = %v", err)
	}
}
