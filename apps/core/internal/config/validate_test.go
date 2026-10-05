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
		{"missing mongo uri", map[string]string{"MONGO_URI": ""}, "MONGO_URI is required"},
		{"advertise addr without host", map[string]string{"CORE_ADVERTISE_ADDR": ":9000"}, "CORE_ADVERTISE_ADDR"},
		{"lowercase stream", map[string]string{"EVT_STREAM": "chatim_evt"}, "EVT_STREAM must match"},
		{"dotted stream", map[string]string{"EVT_STREAM": "CHATIM.EVT"}, "EVT_STREAM must match"},
		{"dotted subject root", map[string]string{"EVT_SUBJECT_ROOT": "evt.x"}, "EVT_SUBJECT_ROOT must match"},
		{"uppercase subject root", map[string]string{"EVT_SUBJECT_ROOT": "EVT"}, "EVT_SUBJECT_ROOT must match"},
		{"wildcard live root", map[string]string{"EVT_LIVE_ROOT": "live>"}, "EVT_LIVE_ROOT must match"},
		{"same roots", map[string]string{"EVT_SUBJECT_ROOT": "evt", "EVT_LIVE_ROOT": "evt"}, "must differ"},
		{"duplicate window above max age", map[string]string{"EVT_STREAM_DUPLICATES": "200h"}, "EVT_*"},
		{"request deadline equals grpc shutdown", map[string]string{"CORE_REQUEST_DEADLINE": "5s"}, "CORE_REQUEST_DEADLINE must be shorter than CORE_GRPC_SHUTDOWN"},
		{"request deadline just under grpc shutdown", map[string]string{"CORE_REQUEST_DEADLINE": "4999ms", "CORE_SHUTDOWN_BUDGET": "30s"}, ""},
		{"queue wait above a tenth of the deadline", map[string]string{"CORE_QUEUE_WAIT": "301ms"}, "CORE_QUEUE_WAIT"},
		{"queue wait at a tenth of the deadline", map[string]string{"CORE_QUEUE_WAIT": "300ms"}, ""},
		{"insert timeout equals request deadline", map[string]string{"FLUSH_INSERT_TIMEOUT": "3s"}, "FLUSH_INSERT_TIMEOUT must be shorter"},
		{"insert timeout just under request deadline", map[string]string{"FLUSH_INSERT_TIMEOUT": "2999ms", "CORE_SHUTDOWN_BUDGET": "30s"}, ""},
		{"redis op timeout above a tenth of the deadline", map[string]string{"REDIS_OP_TIMEOUT": "301ms"}, "REDIS_OP_TIMEOUT must be at most"},
		{"redis op timeout at a tenth of the deadline", map[string]string{"REDIS_OP_TIMEOUT": "300ms"}, ""},
		{"pending ttl within deadline plus 1s", map[string]string{"CID_PENDING_TTL": "4s"}, "CID_PENDING_TTL"},
		{"pending ttl just over deadline plus 1s", map[string]string{"CID_PENDING_TTL": "4001ms"}, ""},
		{"committed ttl below pending ttl", map[string]string{"CID_COMMITTED_TTL": "5s"}, "CID_*"},
		{"core id with a colon", map[string]string{"CORE_ID": "core:a"}, "CORE_ID"},
		{"core id with a space", map[string]string{"CORE_ID": "core a", "CORE_ADVERTISE_ADDR": "core-a:9000"}, "SLOT_*, CORE_ID"},
		{"lease ttl within two ticks", map[string]string{"SLOT_LEASE_TTL": "2s"}, "SLOT_*"},
		{"heartbeat ttl within two ticks", map[string]string{"SLOT_HEARTBEAT_TTL": "2s"}, "SLOT_*"},
		{"hook timeout equals tick", map[string]string{"SLOT_HOOK_TIMEOUT": "1s"}, "SLOT_*"},
		{"slower tick keeps the derived hook timeout valid", map[string]string{"SLOT_TICK": "2s"}, ""},
		{"tick too slow for the heartbeat", map[string]string{"SLOT_TICK": "3s"}, "SLOT_*"},
		{"flush shards above slot count", map[string]string{"FLUSH_SHARDS": "1025"}, "FLUSH_*"},
		{"flush shards at slot count", map[string]string{"FLUSH_SHARDS": "1024"}, ""},
		{"publish shards above slot count", map[string]string{"PUB_SHARDS": "1025"}, "PUB_*"},
		{"write group above flush batch", map[string]string{"ACTOR_MAX_GROUP": "257"}, "ACTOR_MAX_GROUP must not exceed FLUSH_MAX_BATCH"},
		{"write group at flush batch", map[string]string{"ACTOR_MAX_GROUP": "256"}, ""},
		{"ack timeout equals publisher drain", map[string]string{"PUB_ACK_TIMEOUT": "5s"}, "PUB_ACK_TIMEOUT must be shorter"},
		{"ack timeout just under publisher drain", map[string]string{"PUB_ACK_TIMEOUT": "4999ms", "RECONCILE_DELAY": "7s"}, ""},
		{"reconcile delay not under the duplicate window", map[string]string{"RECONCILE_DELAY": "5m"}, "RECONCILE_DELAY must be shorter than EVT_STREAM_DUPLICATES"},
		{"reconcile disabled ignores its delay", map[string]string{"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "5m"}, ""},
		{"reconcile delay within the ack mark deadline", map[string]string{"RECONCILE_DELAY": "3s"}, "RECONCILE_DELAY must be longer than PUB_ACK_TIMEOUT plus the ack mark window and timeout"},
		{"reconcile delay just past the ack mark deadline", map[string]string{"RECONCILE_DELAY": "3011ms"}, ""},
		{"reconcile disabled ignores the ack mark deadline", map[string]string{"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "1s"}, ""},
		{"stop phases fill the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "24200ms"}, "CORE_SHUTDOWN_BUDGET"},
		{"stop phases just fit the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "24201ms"}, ""},
		{"cid batch drain follows the redis op timeout", map[string]string{"REDIS_OP_TIMEOUT": "300ms", "CORE_SHUTDOWN_BUDGET": "24600ms"}, "2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT"},
		{"cid batch shards above slot count", map[string]string{"CID_BATCH_SHARDS": "2000"}, "CID_BATCH_*"},
		{"cid batch shards at slot count", map[string]string{"CID_BATCH_SHARDS": "1024"}, ""},
		{"zero cid batch queue", map[string]string{"CID_BATCH_QUEUE": "0"}, "CID_BATCH_QUEUE"},
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
	setEnv(t, map[string]string{"MONGO_URI": "", "FLUSH_SHARDS": "2000", "EVT_STREAM": "bad", "SLOT_LEASE_TTL": "2s", "PUB_ACK_TIMEOUT": "9s"})
	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil, want validation errors")
	}
	for _, key := range []string{"MONGO_URI", "FLUSH_*", "EVT_STREAM", "SLOT_*", "PUB_ACK_TIMEOUT"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("Load() error %q does not mention %s", err, key)
		}
	}
}

func TestLoadNeverEchoesMongoURI(t *testing.T) {
	secret := "mongodb://chatim:s3cr3t@m1/?replicaSet=rs0"
	for _, broken := range []map[string]string{
		{"MONGO_URI": secret, "FLUSH_SHARDS": "2000"},
		{"MONGO_URI": secret, "REDIS_DB": "x"},
		{"MONGO_URI": secret, "CORE_ID": "core a"},
	} {
		setEnv(t, broken)
		_, err := config.Load()
		if err == nil || strings.Contains(err.Error(), "s3cr3t") {
			t.Fatalf("Load() = %v, want an error that hides the mongo credentials", err)
		}
	}
}
