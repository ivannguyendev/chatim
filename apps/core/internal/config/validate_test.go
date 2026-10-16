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
		{"lowercase timer stream", map[string]string{"TIMER_STREAM": "chatim_timer"}, "TIMER_STREAM must match"},
		{"timer stream named like the event stream", map[string]string{"TIMER_STREAM": "CHATIM_EVT"}, "TIMER_STREAM must differ from EVT_STREAM"},
		{"timer stream named like the old work stream", map[string]string{"TIMER_STREAM": "CHATIM_WORK"}, ""},
		{"dotted timer root", map[string]string{"TIMER_SUBJECT_ROOT": "timer.x"}, "TIMER_SUBJECT_ROOT must match"},
		{"timer root equals the event root", map[string]string{"TIMER_SUBJECT_ROOT": "evt"}, "TIMER_SUBJECT_ROOT must differ"},
		{"timer root equals the live root", map[string]string{"TIMER_SUBJECT_ROOT": "live"}, "TIMER_SUBJECT_ROOT must differ"},
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
		{"ack timeout just under publisher drain", map[string]string{"PUB_ACK_TIMEOUT": "4999ms"}, ""},
		{"sweep min age within ack timeout plus clear flush plus clear timeout", map[string]string{"SWEEP_MIN_AGE": "9s"}, "SWEEP_MIN_AGE must be longer than PUB_ACK_TIMEOUT + STATE_CLEAR_FLUSH + STATE_CLEAR_TIMEOUT"},
		{"sweep min age just past ack timeout plus clear flush plus clear timeout", map[string]string{"SWEEP_MIN_AGE": "9001ms"}, ""},
		{"clear flush counts in the sweep min age", map[string]string{"STATE_CLEAR_FLUSH": "3s", "SWEEP_MIN_AGE": "10s"}, "SWEEP_MIN_AGE must be longer than PUB_ACK_TIMEOUT + STATE_CLEAR_FLUSH + STATE_CLEAR_TIMEOUT"},
		{"clear timeout counts in the sweep min age", map[string]string{"STATE_CLEAR_TIMEOUT": "11s"}, "SWEEP_MIN_AGE must be longer than PUB_ACK_TIMEOUT + STATE_CLEAR_FLUSH + STATE_CLEAR_TIMEOUT"},
		{"clear timeout just inside the sweep min age", map[string]string{"STATE_CLEAR_TIMEOUT": "10999ms"}, ""},
		{"sweep min age plus interval reach the duplicate window", map[string]string{"EVT_STREAM_DUPLICATES": "7s"}, "SWEEP_MIN_AGE + SWEEP_INTERVAL must be shorter than EVT_STREAM_DUPLICATES"},
		{"sweep min age plus interval just under the duplicate window", map[string]string{"EVT_STREAM_DUPLICATES": "17001ms"}, ""},
		{"sweep lookback within request deadline plus 10s", map[string]string{"CORE_REQUEST_DEADLINE": "4s", "PUB_ACK_TIMEOUT": "1s", "STATE_CLEAR_FLUSH": "500ms", "STATE_CLEAR_TIMEOUT": "1s", "SWEEP_MIN_AGE": "3s", "SWEEP_LOOKBACK": "14s"}, "SWEEP_LOOKBACK must be longer than CORE_REQUEST_DEADLINE + 10s"},
		{"sweep lookback just past request deadline plus 10s", map[string]string{"CORE_REQUEST_DEADLINE": "4s", "PUB_ACK_TIMEOUT": "1s", "STATE_CLEAR_FLUSH": "500ms", "STATE_CLEAR_TIMEOUT": "1s", "SWEEP_MIN_AGE": "3s", "SWEEP_LOOKBACK": "14001ms"}, ""},
		{"sweep lookback within min age plus 10s", map[string]string{"SWEEP_LOOKBACK": "25s"}, "SWEEP_LOOKBACK must be longer than SWEEP_MIN_AGE + 10s"},
		{"sweep lookback just past min age plus 10s", map[string]string{"SWEEP_LOOKBACK": "25001ms"}, ""},
		{"zero state clear timeout", map[string]string{"STATE_CLEAR_TIMEOUT": "0"}, "STATE_CLEAR_TIMEOUT"},
		{"sweep batch above the pending page", map[string]string{"SWEEP_BATCH": "1001"}, "SWEEP_*"},
		{"sweep batch at the pending page", map[string]string{"SWEEP_BATCH": "1000"}, ""},
		{"zero sweep rate", map[string]string{"SWEEP_RATE": "0"}, "SWEEP_RATE"},
		{"state clear batch above its queue", map[string]string{"STATE_CLEAR_BATCH": "70000"}, "STATE_CLEAR_*"},
		{"zero state clear queue", map[string]string{"STATE_CLEAR_QUEUE": "0"}, "STATE_CLEAR_QUEUE"},
		{"stop phases fill the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "26200ms"}, "CORE_SHUTDOWN_BUDGET"},
		{"stop phases just fit the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "26201ms"}, ""},
		{"cid batch drain follows the redis op timeout", map[string]string{"REDIS_OP_TIMEOUT": "300ms", "CORE_SHUTDOWN_BUDGET": "26600ms"}, "2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT"},
		{"zero projection fetch batch", map[string]string{"PROJECTION_FETCH_BATCH": "0"}, "PROJECTION_FETCH_BATCH"},
		{"projection fetch batch above the limit", map[string]string{"PROJECTION_FETCH_BATCH": "8193"}, "PROJECTION_*"},
		{"projection fetch batch at the limit", map[string]string{"PROJECTION_FETCH_BATCH": "8192"}, ""},
		{"projection fetch wait past the stop phase", map[string]string{"PROJECTION_FETCH_WAIT": "1001ms"}, "PROJECTION_*"},
		{"projection fetch wait at the stop phase", map[string]string{"PROJECTION_FETCH_WAIT": "1s"}, ""},
		{"zero projection fetch wait", map[string]string{"PROJECTION_FETCH_WAIT": "0s"}, "PROJECTION_FETCH_WAIT"},
		{"old room activity fetch variables are ignored", map[string]string{"PROJECTION_ACTIVITY_FETCH_BATCH": "junk", "PROJECTION_ACTIVITY_FETCH_WAIT": "-1s"}, ""},
		{"old engine variables are ignored", map[string]string{
			"RECONCILE_ENABLED": "maybe", "RECONCILE_DELAY": "junk", "RECONCILE_DRAIN": "30s", "WORK_FETCH_BATCH": "0", "WORK_DRAIN": "-1s", "EVT_ACK_MARK_TTL": "x",
		}, ""},
		{"old work stream variables are ignored", map[string]string{
			"WORK_STREAM": "CHATIM_EVT", "WORK_SUBJECT_ROOT": "evt.x", "WORK_PARTITIONS": "junk", "WORK_MAX_AGE": "-1h", "WORK_DUPLICATES": "3h",
		}, ""},
		{"cid batch shards above slot count", map[string]string{"CID_BATCH_SHARDS": "2000"}, "CID_BATCH_*"},
		{"cid batch shards at slot count", map[string]string{"CID_BATCH_SHARDS": "1024"}, ""},
		{"zero cid batch queue", map[string]string{"CID_BATCH_QUEUE": "0"}, "CID_BATCH_QUEUE"},
		{"pin limit above the cap", map[string]string{"PIN_LIMIT": "1001"}, "REACTION_EMOJIS, PIN_LIMIT"},
		{"pin limit at the cap", map[string]string{"PIN_LIMIT": "1000"}, ""},
		{"room tail cache under 1MB", map[string]string{"ROOM_TAIL_CACHE_BYTES": "1048575"}, "ROOM_TAIL_CACHE_BYTES must be at least 1048576"},
		{"room tail cache at 1MB", map[string]string{"ROOM_TAIL_CACHE_BYTES": "1048576"}, ""},
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
