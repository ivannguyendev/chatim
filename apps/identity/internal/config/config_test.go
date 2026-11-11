package config_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/config"
)

var kek = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("IDENTITY_MONGO_URI", "mongodb://chatim-mongodb:27017/?replicaSet=rs0")
	t.Setenv("IDENTITY_MONGO_USER", "identity")
	t.Setenv("IDENTITY_MONGO_PASSWORD", "mongo-secret-value")
	t.Setenv("IDENTITY_ISSUER", "chatim-dev")
	t.Setenv("IDENTITY_AUDIENCE", "chatim-websocket")
	t.Setenv("IDENTITY_EXCHANGE_AUDIENCE", "chatim")
	t.Setenv("IDENTITY_KEY_ENCRYPTION_KEY", kek)
	t.Setenv("IDENTITY_NODE_SECRET", nodeSecret)
}

const nodeSecret = "node-secret-value-0123456789abcdef"

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ListenAddr != ":8080" || c.AdminAddr != ":9090" || c.TokenTTL != 5*time.Minute || c.BodyMax != 128<<10 {
		t.Fatalf("defaults = %+v", c)
	}
	if c.InternalAddr != ":8081" || c.KeyActivateLead != 2*time.Minute || c.NodeSecret != nodeSecret || c.RevocationKeep() != 6*time.Minute {
		t.Fatalf("internal defaults = %q %v %v", c.InternalAddr, c.KeyActivateLead, c.RevocationKeep())
	}
	if c.DirectoryLoadSlots != 4 || c.InflightMax != 10000 || c.UserRate != 50 ||
		c.UserBurst != 100 || c.UserInflight != 32 || c.UserLimiters != 100000 {
		t.Fatalf("directory defaults = %d %d %d %d %d %d", c.DirectoryLoadSlots, c.InflightMax, c.UserRate, c.UserBurst, c.UserInflight, c.UserLimiters)
	}
	if c.ExchangeAudience != "chatim" || c.ExchangeMaxLifetime != 10*time.Minute || c.TrustReload != 10*time.Second ||
		c.ExchangeIPRate != 50 || c.ExchangeIPBurst != 500 || c.ExchangeIPTracked != 100000 {
		t.Fatalf("exchange defaults = %q %v %v %d %d %d", c.ExchangeAudience, c.ExchangeMaxLifetime, c.TrustReload, c.ExchangeIPRate, c.ExchangeIPBurst, c.ExchangeIPTracked)
	}
	if len(c.KeyEncryptionKey) != 32 {
		t.Fatalf("kek length = %d, want 32", len(c.KeyEncryptionKey))
	}
	if c.MongoURI != "mongodb://chatim-mongodb:27017/?replicaSet=rs0" || c.MongoDB != "chatim_identity" || c.MongoUser != "identity" ||
		c.MongoPassword != "mongo-secret-value" || c.MongoAuthSource != "chatim_identity" {
		t.Fatalf("mongo settings = %q %q %q %q", c.MongoURI, c.MongoDB, c.MongoUser, c.MongoAuthSource)
	}
}

func TestLogValueHidesSecrets(t *testing.T) {
	setRequired(t)
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := c.String()
	for _, secret := range []string{"mongo-secret-value", kek, nodeSecret} {
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
	t.Setenv("IDENTITY_MONGO_PASSWORD", "")
	t.Setenv("IDENTITY_MONGO_PASSWORD_FILE", pw)
	t.Setenv("IDENTITY_KEY_ENCRYPTION_KEY", "")
	t.Setenv("IDENTITY_KEY_ENCRYPTION_KEY_FILE", key)
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.MongoPassword != "from-file" || len(c.KeyEncryptionKey) != 32 {
		t.Fatal("secrets were not read from files")
	}
}

func TestEachPortCanBeTurnedOff(t *testing.T) {
	t.Run("public off", func(t *testing.T) {
		setRequired(t)
		t.Setenv("IDENTITY_ADDR", "")
		c, err := config.Load()
		if err != nil || c.ListenAddr != "" || c.InternalAddr != ":8081" {
			t.Fatalf("Load = %+v, %v; want only the internal port", c, err)
		}
	})
	t.Run("internal off", func(t *testing.T) {
		setRequired(t)
		t.Setenv("IDENTITY_INTERNAL_ADDR", "")
		t.Setenv("IDENTITY_NODE_SECRET", "")
		c, err := config.Load()
		if err != nil || c.InternalAddr != "" || c.ListenAddr != ":8080" {
			t.Fatalf("Load = %+v, %v; want only the public port", c, err)
		}
	})
}

func TestLoadRejectsBadConfig(t *testing.T) {
	cases := map[string]map[string]string{
		"missing issuer":      {"IDENTITY_ISSUER": ""},
		"missing kek":         {"IDENTITY_KEY_ENCRYPTION_KEY": ""},
		"short kek":           {"IDENTITY_KEY_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString([]byte("short"))},
		"kek not base64":      {"IDENTITY_KEY_ENCRYPTION_KEY": "!!!"},
		"missing mongo uri":   {"IDENTITY_MONGO_URI": ""},
		"same ports":          {"IDENTITY_ADMIN_ADDR": ":8080"},
		"bad duration":        {"IDENTITY_TOKEN_TTL": "soon"},
		"budget too small":    {"IDENTITY_SHUTDOWN_BUDGET": "1s"},
		"bad cors origin":     {"IDENTITY_CORS_ORIGINS": "app.example.com"},
		"both ports off":      {"IDENTITY_ADDR": "", "IDENTITY_INTERNAL_ADDR": ""},
		"internal on admin":   {"IDENTITY_INTERNAL_ADDR": ":9090"},
		"internal on public":  {"IDENTITY_INTERNAL_ADDR": ":8080"},
		"missing node secret": {"IDENTITY_NODE_SECRET": ""},
		"short node secret":   {"IDENTITY_NODE_SECRET": "short"},
		"negative lead":       {"IDENTITY_KEY_ACTIVATE_LEAD": "-1s"},
		"zero lead":           {"IDENTITY_KEY_ACTIVATE_LEAD": "0s"},
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

func TestLoadRefusesTimersThatBreakNodes(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"lead shorter than reload plus node keys interval": {map[string]string{"IDENTITY_KEY_ACTIVATE_LEAD": "89s"}, "IDENTITY_KEY_ACTIVATE_LEAD 1m29s must be at least IDENTITY_KEY_RELOAD + 1m0s = 1m30s"},
		"token ttl past the node revocation cap":           {map[string]string{"IDENTITY_TOKEN_TTL": "3h"}, "IDENTITY_TOKEN_TTL 3h0m0s + 1m0s must not exceed 2h0m0s"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want an error containing %q", err, tc.want)
			}
		})
	}
	setRequired(t)
	t.Setenv("IDENTITY_KEY_ACTIVATE_LEAD", "90s")
	t.Setenv("IDENTITY_TOKEN_TTL", "119m")
	if _, err := config.Load(); err != nil {
		t.Fatalf("Load at both limits = %v", err)
	}
}

func TestLoadRefusesBadDirectoryLimits(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"zero load slots":             {map[string]string{"IDENTITY_DIRECTORY_LOAD_SLOTS": "0"}, "IDENTITY_DIRECTORY_LOAD_SLOTS 0 must be at least 1"},
		"negative load slots":         {map[string]string{"IDENTITY_DIRECTORY_LOAD_SLOTS": "-1"}, "IDENTITY_DIRECTORY_LOAD_SLOTS -1 must be at least 1"},
		"zero user rate":              {map[string]string{"IDENTITY_USER_RATE": "0"}, "IDENTITY_USER_RATE must be positive"},
		"zero user burst":             {map[string]string{"IDENTITY_USER_BURST": "0"}, "IDENTITY_USER_BURST must be positive"},
		"zero user inflight":          {map[string]string{"IDENTITY_USER_INFLIGHT": "0"}, "IDENTITY_USER_INFLIGHT must be positive"},
		"zero user limiters":          {map[string]string{"IDENTITY_USER_LIMITERS": "0"}, "IDENTITY_USER_LIMITERS must be positive"},
		"zero inflight max":           {map[string]string{"IDENTITY_INFLIGHT_MAX": "0"}, "IDENTITY_INFLIGHT_MAX must be positive"},
		"limiters under the node cap": {map[string]string{"IDENTITY_USER_LIMITERS": "9999"}, "IDENTITY_USER_LIMITERS 9999 must be at least IDENTITY_INFLIGHT_MAX 10000"},
		"unparsable directory number": {map[string]string{"IDENTITY_USER_RATE": "many"}, "IDENTITY_USER_RATE"},
		"zero body size":              {map[string]string{"APP_BODY_SIZE_MAX": "0"}, "APP_BODY_SIZE_MAX must be positive"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want an error containing %q", err, tc.want)
			}
			if strings.Contains(tc.want, "LOAD_SLOTS") && strings.Count(err.Error(), "IDENTITY_DIRECTORY_LOAD_SLOTS") != 1 {
				t.Fatalf("Load = %v, want one IDENTITY_DIRECTORY_LOAD_SLOTS error", err)
			}
		})
	}
	setRequired(t)
	t.Setenv("IDENTITY_DIRECTORY_LOAD_SLOTS", "1")
	t.Setenv("IDENTITY_USER_LIMITERS", "10000")
	if _, err := config.Load(); err != nil {
		t.Fatalf("Load at the directory limits = %v", err)
	}
	t.Setenv("IDENTITY_DIRECTORY_LOAD_SLOTS", "64")
	if _, err := config.Load(); err != nil {
		t.Fatalf("Load with many load slots = %v", err)
	}
}

func TestLogValueListsDirectorySettings(t *testing.T) {
	setRequired(t)
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := c.String()
	for _, key := range []string{"directory_load_slots=4", "inflight_max=10000", "user_rate=50", "user_burst=100", "user_inflight=32", "user_limiters=100000"} {
		if !strings.Contains(out, key) {
			t.Fatalf("config log lacks %s: %s", key, out)
		}
	}
}

func TestBodySizeComesFromTheSharedSetting(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_BODY_SIZE_MAX", "4096")
	c, err := config.Load()
	if err != nil || c.BodyMax != 4096 {
		t.Fatalf("Load = %d, %v; want BodyMax 4096", c.BodyMax, err)
	}
}

func TestRemovedLoginAndAdminSettingsAreNotRead(t *testing.T) {
	setRequired(t)
	for _, key := range []string{
		"IDENTITY_PUBLIC_URL", "IDENTITY_SESSION_IDLE", "IDENTITY_SESSION_MAX", "IDENTITY_REFRESH_GRACE", "IDENTITY_DPOP_WINDOW", "IDENTITY_HASH_CONCURRENCY",
		"IDENTITY_LOGIN_FREE_FAILURES", "IDENTITY_LOGIN_MAX_DELAY", "IDENTITY_LOGIN_IP_RATE", "IDENTITY_LOGIN_IP_BURST", "IDENTITY_LOGIN_IP_TRACKED",
		"IDENTITY_PASSWORD_MIN", "IDENTITY_ACCESS_TTL", "IDENTITY_ADMIN_USER_RATE", "IDENTITY_ADMIN_USER_BURST", "IDENTITY_ADMIN_INFLIGHT_MAX", "IDENTITY_ADMIN_DB_SLOTS",
		"IDENTITY_PG_URI", "IDENTITY_PG_PASSWORD", "IDENTITY_PG_MAX_CONNS", "IDENTITY_FEED_WAIT_MAX", "IDENTITY_BODY_MAX",
	} {
		t.Setenv(key, "abc")
	}
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load with removed settings set to garbage = %v", err)
	}
	out := c.String()
	for _, key := range []string{"public_url", "session_idle", "dpop_window", "hash_concurrency", "login_", "password_min", "access_ttl", "admin_user", "admin_inflight", "admin_db_slots"} {
		if strings.Contains(out, key) {
			t.Fatalf("config log still lists %s: %s", key, out)
		}
	}
}

func TestLoadRefusesBadExchangeSettings(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"missing exchange audience":     {map[string]string{"IDENTITY_EXCHANGE_AUDIENCE": ""}, "IDENTITY_EXCHANGE_AUDIENCE is required"},
		"exchange audience of chat":     {map[string]string{"IDENTITY_EXCHANGE_AUDIENCE": "chatim-websocket"}, "IDENTITY_EXCHANGE_AUDIENCE must differ from IDENTITY_AUDIENCE"},
		"zero token ttl":                {map[string]string{"IDENTITY_TOKEN_TTL": "0s"}, "IDENTITY_TOKEN_TTL must be positive"},
		"zero exchange max lifetime":    {map[string]string{"IDENTITY_EXCHANGE_MAX_LIFETIME": "0s"}, "IDENTITY_EXCHANGE_MAX_LIFETIME must be positive"},
		"zero trust reload":             {map[string]string{"IDENTITY_TRUST_RELOAD": "0s"}, "IDENTITY_TRUST_RELOAD must be positive"},
		"trust reload over a minute":    {map[string]string{"IDENTITY_TRUST_RELOAD": "61s"}, "IDENTITY_TRUST_RELOAD 1m1s must not exceed 1m0s"},
		"zero exchange ip rate":         {map[string]string{"IDENTITY_EXCHANGE_IP_RATE": "0"}, "IDENTITY_EXCHANGE_IP_RATE must be positive"},
		"zero exchange ip burst":        {map[string]string{"IDENTITY_EXCHANGE_IP_BURST": "0"}, "IDENTITY_EXCHANGE_IP_BURST must be positive"},
		"zero exchange ip tracked":      {map[string]string{"IDENTITY_EXCHANGE_IP_TRACKED": "0"}, "IDENTITY_EXCHANGE_IP_TRACKED must be positive"},
		"unparsable exchange ip number": {map[string]string{"IDENTITY_EXCHANGE_IP_RATE": "many"}, "IDENTITY_EXCHANGE_IP_RATE"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want an error containing %q", err, tc.want)
			}
		})
	}
	setRequired(t)
	t.Setenv("IDENTITY_TRUST_RELOAD", "1m")
	if _, err := config.Load(); err != nil {
		t.Fatalf("Load at the trust reload limit = %v", err)
	}
}

func TestLogValueListsExchangeSettings(t *testing.T) {
	setRequired(t)
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := c.String()
	for _, key := range []string{"token_ttl=5m0s", "exchange_audience=chatim", "exchange_max_lifetime=10m0s", "trust_reload=10s", "exchange_ip_rate=50", "exchange_ip_burst=500", "exchange_ip_tracked=100000"} {
		if !strings.Contains(out, key) {
			t.Fatalf("config log lacks %s: %s", key, out)
		}
	}
}
