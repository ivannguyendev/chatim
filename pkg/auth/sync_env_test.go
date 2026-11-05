package auth_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/auth"
)

func TestFromEnvDefaultsNeedTheIdentityURLs(t *testing.T) {
	c, err := auth.SyncConfigFromEnv("WS")
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.InternalURL != "" || c.JWKSURL != "" || c.RevocationsEvery != auth.DefaultRevocationsEvery || c.KeysEvery != auth.DefaultKeysEvery ||
		c.ReadyTimeout != auth.DefaultReadyTimeout || c.HTTPTimeout != auth.DefaultHTTPTimeout || c.BodyMax != auth.DefaultBodyMax ||
		c.SharedTTL != auth.DefaultSharedTTL || c.KeysMinGap != auth.DefaultKeysMinGap || c.AllowHTTP {
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
	t.Setenv("GW_IDENTITY_INTERNAL_URL", internalURL)
	t.Setenv("GW_IDENTITY_JWKS_URL", "http://identity:8080/.well-known/jwks.json")
	t.Setenv("GW_IDENTITY_SECRET_FILE", path)
	t.Setenv("GW_IDENTITY_REVOCATIONS_INTERVAL", "3s")
	t.Setenv("GW_IDENTITY_KEYS_INTERVAL", "10s")
	t.Setenv("GW_IDENTITY_READY_TIMEOUT", "4s")
	t.Setenv("GW_IDENTITY_ALLOW_HTTP", "true")
	c, err := auth.SyncConfigFromEnv("GW")
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
	t.Setenv("WS_IDENTITY_REVOCATIONS_INTERVAL", "soon")
	t.Setenv("WS_IDENTITY_ALLOW_HTTP", "maybe")
	if _, err := auth.SyncConfigFromEnv("WS"); err == nil || !strings.Contains(err.Error(), "WS_IDENTITY_REVOCATIONS_INTERVAL") || !strings.Contains(err.Error(), "WS_IDENTITY_ALLOW_HTTP") {
		t.Fatalf("FromEnv err = %v", err)
	}
}
