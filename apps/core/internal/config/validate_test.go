package config_test

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"defaults are valid", nil, ""},
		{"missing mongo uri", map[string]string{"MONGO_URI": ""}, "MONGO_URI"},
		{"negative redis db", map[string]string{"REDIS_DB": "-1"}, "REDIS_DB"},
		{"zero stream replicas", map[string]string{"EVT_STREAM_REPLICAS": "0"}, "EVT_STREAM_REPLICAS"},
		{"zero flush batch", map[string]string{"FLUSH_MAX_BATCH": "0"}, "FLUSH_MAX_BATCH"},
		{"negative flush shards", map[string]string{"FLUSH_SHARDS": "-1"}, "FLUSH_SHARDS"},
		{"zero mailbox", map[string]string{"ACTOR_MAILBOX": "0"}, "ACTOR_MAILBOX"},
		{"zero max inflight", map[string]string{"CORE_MAX_INFLIGHT": "0"}, "CORE_MAX_INFLIGHT"},
		{"zero flush window", map[string]string{"FLUSH_WINDOW": "0s"}, "FLUSH_WINDOW"},
		{"negative actor idle", map[string]string{"ACTOR_IDLE": "-1s"}, "ACTOR_IDLE"},
		{"zero request deadline", map[string]string{"CORE_REQUEST_DEADLINE": "0s"}, "CORE_REQUEST_DEADLINE"},
		{"zero drain delay", map[string]string{"CORE_DRAIN_DELAY": "0s"}, "CORE_DRAIN_DELAY"},
		{"zero grpc shutdown", map[string]string{"CORE_GRPC_SHUTDOWN": "0s"}, "CORE_GRPC_SHUTDOWN"},
		{"zero publisher drain", map[string]string{"CORE_PUBLISHER_DRAIN": "0s"}, "CORE_PUBLISHER_DRAIN"},
		{"zero shutdown budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "0s"}, "CORE_SHUTDOWN_BUDGET"},
		{"lowercase stream", map[string]string{"EVT_STREAM": "chatim_evt"}, "EVT_STREAM"},
		{"dotted stream", map[string]string{"EVT_STREAM": "CHATIM.EVT"}, "EVT_STREAM"},
		{"dotted subject root", map[string]string{"EVT_SUBJECT_ROOT": "evt.x"}, "EVT_SUBJECT_ROOT"},
		{"uppercase subject root", map[string]string{"EVT_SUBJECT_ROOT": "EVT"}, "EVT_SUBJECT_ROOT"},
		{"wildcard live root", map[string]string{"EVT_LIVE_ROOT": "live>"}, "EVT_LIVE_ROOT"},
		{"same roots", map[string]string{"EVT_SUBJECT_ROOT": "evt", "EVT_LIVE_ROOT": "evt"}, "must differ"},
		{"request deadline equals grpc shutdown", map[string]string{"CORE_REQUEST_DEADLINE": "10s"}, "CORE_REQUEST_DEADLINE"},
		{"request deadline just under grpc shutdown", map[string]string{"CORE_REQUEST_DEADLINE": "9999ms"}, ""},
		{"stop phases fill the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "17s"}, "CORE_SHUTDOWN_BUDGET"},
		{"stop phases just fit the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "17001ms"}, ""},
		{"stop phases overflow", map[string]string{
			"CORE_DRAIN_DELAY": "1000000h", "CORE_GRPC_SHUTDOWN": "1000000h", "CORE_PUBLISHER_DRAIN": "1000000h",
		}, "CORE_SHUTDOWN_BUDGET"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			_, err := config.Load()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() = %v, want error mentioning %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadReportsEveryBrokenRule(t *testing.T) {
	setEnv(t, map[string]string{"MONGO_URI": "", "FLUSH_SHARDS": "0", "EVT_STREAM": "bad"})
	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil, want validation errors")
	}
	for _, key := range []string{"MONGO_URI", "FLUSH_SHARDS", "EVT_STREAM"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("Load() error %q does not mention %s", err, key)
		}
	}
}

func TestLoadNeverEchoesMongoURI(t *testing.T) {
	secret := "mongodb://chatim:s3cr3t@m1/?replicaSet=rs0"
	setEnv(t, map[string]string{"MONGO_URI": secret, "FLUSH_SHARDS": "0"})
	_, err := config.Load()
	if err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("Load() = %v, want an error that hides the mongo credentials", err)
	}
}
