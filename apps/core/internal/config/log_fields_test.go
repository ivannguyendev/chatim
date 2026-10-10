package config_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

var secretLooking = []string{"password", "secret", "token", "credential", "uri", "url"}

func loggedConfig(t *testing.T) map[string]any {
	t.Helper()
	setEnv(t, nil)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("starting core", "config", cfg)
	var line struct{ Config map[string]any }
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	return line.Config
}

func TestConfigLogValueLogsEveryField(t *testing.T) {
	logged := loggedConfig(t)
	secrets := config.SecretFieldNames()
	var walk func(typ reflect.Type, prefix string, group map[string]any)
	walk = func(typ reflect.Type, prefix string, group map[string]any) {
		want := 0
		for f := range typ.Fields() {
			if f.Type.Kind() == reflect.Func || !f.IsExported() {
				continue
			}
			want++
			if prefix == "" && slices.Contains(secrets, f.Name) {
				continue
			}
			got, ok := group[config.SnakeCase(f.Name)]
			if !ok {
				t.Errorf("field %s%s is not logged; logged %v", prefix, f.Name, group)
				continue
			}
			if f.Type.Kind() == reflect.Struct {
				sub, _ := got.(map[string]any)
				walk(f.Type, prefix+f.Name+".", sub)
			}
		}
		if len(group) != want {
			t.Errorf("group %q logs %d keys, want %d: %v", prefix, len(group), want, group)
		}
	}
	walk(reflect.TypeFor[config.Config](), "", logged)
	for _, key := range []string{"core_id", "grpc_addr", "cid_batch", "nats_url", "mongo_auth", "locked_message_kinds", "member_count_check_delay"} {
		if _, ok := logged[key]; !ok {
			t.Errorf("summary misses %s: %v", key, logged)
		}
	}
	if limits, _ := logged["limits"].(map[string]any); limits["pin_limit"] != float64(50) || !strings.Contains(limits["emojis"].(string), "👍") {
		t.Errorf("limits = %v, want pin_limit 50 and the default emojis", limits)
	}
}

func TestSecretLookingFieldsAreRedacted(t *testing.T) {
	secrets := config.SecretFieldNames()
	seen := map[string]bool{}
	var walk func(typ reflect.Type, prefix string)
	walk = func(typ reflect.Type, prefix string) {
		for f := range typ.Fields() {
			path := prefix + f.Name
			seen[path] = true
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type, path+".")
				continue
			}
			lower := strings.ToLower(f.Name)
			for _, word := range secretLooking {
				if strings.Contains(lower, word) && !slices.Contains(secrets, path) {
					t.Errorf("field %s looks secret but has no redaction in secretFields", path)
				}
			}
		}
	}
	walk(reflect.TypeFor[config.Config](), "")
	for _, name := range secrets {
		if !seen[name] {
			t.Errorf("secretFields names %s, which is not a Config field", name)
		}
	}
}

func TestSnakeCaseKeepsAcronymsTogether(t *testing.T) {
	for name, want := range map[string]string{
		"CoreID": "core_id", "GRPCAddr": "grpc_addr", "CIDBatch": "cid_batch", "MongoDB": "mongo_db",
		"HeartbeatTTL": "heartbeat_ttl", "TTL": "ttl", "MaxKeys": "max_keys", "ReconcileEnabled": "reconcile_enabled",
	} {
		if got := config.SnakeCase(name); got != want {
			t.Errorf("SnakeCase(%q) = %q, want %q", name, got, want)
		}
	}
}
