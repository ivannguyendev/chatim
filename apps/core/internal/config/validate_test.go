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
		{"lowercase work stream", map[string]string{"WORK_STREAM": "chatim_work"}, "WORK_STREAM must match"},
		{"work stream named like the event stream", map[string]string{"WORK_STREAM": "CHATIM_EVT"}, "WORK_STREAM must differ from EVT_STREAM"},
		{"dotted work root", map[string]string{"WORK_SUBJECT_ROOT": "work.x"}, "WORK_SUBJECT_ROOT must match"},
		{"work root equals the event root", map[string]string{"WORK_SUBJECT_ROOT": "evt"}, "WORK_SUBJECT_ROOT must differ"},
		{"work root equals the live root", map[string]string{"WORK_SUBJECT_ROOT": "live"}, "WORK_SUBJECT_ROOT must differ"},
		{"work partitions above slot count", map[string]string{"WORK_PARTITIONS": "1025"}, "WORK_*"},
		{"work partitions at slot count", map[string]string{"WORK_PARTITIONS": "1024"}, ""},
		{"work duplicate window above max age", map[string]string{"WORK_DUPLICATES": "3h"}, "WORK_*"},
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
		{"effect delay rule holds with the reader off", map[string]string{"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "5m"}, "RECONCILE_DELAY must be shorter than EVT_STREAM_DUPLICATES"},
		{"reconcile delay within the ack mark deadline", map[string]string{"RECONCILE_DELAY": "3s"}, "RECONCILE_DELAY must be longer than PUB_ACK_TIMEOUT plus the ack mark window and timeout"},
		{"reconcile delay just past the ack mark deadline", map[string]string{"RECONCILE_DELAY": "3011ms"}, ""},
		{"ack mark deadline holds with the reader off", map[string]string{"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "1s"}, "RECONCILE_DELAY must be longer than PUB_ACK_TIMEOUT plus the ack mark window and timeout"},
		{"work duplicates within confirm plus drain", map[string]string{"WORK_DUPLICATES": "2s"}, "WORK_DUPLICATES must be longer than RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN"},
		{"work duplicates just past confirm plus drain", map[string]string{"WORK_DUPLICATES": "2001ms"}, ""},
		{"stop phases fill the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "26200ms"}, "CORE_SHUTDOWN_BUDGET"},
		{"stop phases just fit the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "26201ms"}, ""},
		{"cid batch drain follows the redis op timeout", map[string]string{"REDIS_OP_TIMEOUT": "300ms", "CORE_SHUTDOWN_BUDGET": "26600ms"}, "2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT"},
		{"worker drain counts in the stop plan", map[string]string{"WORK_DRAIN": "1001ms", "CORE_SHUTDOWN_BUDGET": "26201ms"}, "WORK_DRAIN + 1s"},
		{"zero fetch batch", map[string]string{"WORK_FETCH_BATCH": "0"}, "WORK_FETCH_BATCH"},
		{"fetch batch above the consumer max ack pending", map[string]string{"WORK_FETCH_BATCH": "1025"}, "WORK_*"},
		{"fetch batch at the consumer max ack pending", map[string]string{"WORK_FETCH_BATCH": "1024"}, ""},
		{"cid batch shards above slot count", map[string]string{"CID_BATCH_SHARDS": "2000"}, "CID_BATCH_*"},
		{"cid batch shards at slot count", map[string]string{"CID_BATCH_SHARDS": "1024"}, ""},
		{"zero cid batch queue", map[string]string{"CID_BATCH_QUEUE": "0"}, "CID_BATCH_QUEUE"},
		{"pin limit above the cap", map[string]string{"PIN_LIMIT": "1001"}, "REACTION_EMOJIS, PIN_LIMIT"},
		{"pin limit at the cap", map[string]string{"PIN_LIMIT": "1000"}, ""},
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

func TestCountCheckDelaysMustOutliveARequest(t *testing.T) {
	for _, key := range []string{"MEMBER_COUNT_CHECK_DELAY", "MESSAGE_COUNT_CHECK_DELAY"} {
		tests := []struct {
			env     map[string]string
			wantErr bool
		}{
			{map[string]string{key: "3s"}, true},
			{map[string]string{key: "2s"}, true},
			{map[string]string{key: "3001ms"}, false},
			{map[string]string{key: "4s", "CORE_REQUEST_DEADLINE": "4s", "CORE_SHUTDOWN_BUDGET": "30s"}, true},
			{map[string]string{"CORE_REQUEST_DEADLINE": "4999ms", "CORE_SHUTDOWN_BUDGET": "30s"}, false},
		}
		for _, tt := range tests {
			setEnv(t, tt.env)
			_, err := config.Load()
			failed := err != nil && strings.Contains(err.Error(), key+" must be longer than CORE_REQUEST_DEADLINE")
			if failed != tt.wantErr || (!tt.wantErr && err != nil) {
				t.Fatalf("%v: Load() = %v; want rule broken %v", tt.env, err, tt.wantErr)
			}
		}
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
