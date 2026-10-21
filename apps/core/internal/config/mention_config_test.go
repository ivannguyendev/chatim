package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func TestMentionStopsInParallelWithTheProjections(t *testing.T) {
	setEnv(t, map[string]string{"PROJECTION_FETCH_WAIT": "500ms", "MENTION_FETCH_WAIT": "500ms"})
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.StopPlan(); got.Mention != 1500*time.Millisecond || got.Total() != 27200*time.Millisecond {
		t.Errorf("mention phase %v plan %v, want 1.5s counted once in 27.2s", got.Mention, got.Total())
	}
}

func TestDisabledMentionHasNoStopPhaseNorRules(t *testing.T) {
	setEnv(t, map[string]string{"MENTION_ENABLED": "false", "MENTION_WS_PUBLISH_STREAM": "CHATIM_EVT", "MENTION_FETCH_WAIT": "10s"})
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Mention.Enabled || c.StopPlan().Mention != 0 {
		t.Errorf("mention enabled %v stop phase %v, want off and none", c.Mention.Enabled, c.StopPlan().Mention)
	}
}

func TestMentionRules(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"ws publish stream named like the event stream", map[string]string{"MENTION_WS_PUBLISH_STREAM": "CHATIM_EVT"}, "MENTION_WS_PUBLISH_STREAM must differ"},
		{"ws publish stream named like the timer stream", map[string]string{"MENTION_WS_PUBLISH_STREAM": "CHATIM_TIMER"}, "MENTION_WS_PUBLISH_STREAM must differ"},
		{"ws publish root under the event root", map[string]string{"MENTION_WS_PUBLISH_ROOT": "evt.publish"}, "MENTION_WS_PUBLISH_ROOT must not start"},
		{"ws publish root equals the live root", map[string]string{"MENTION_WS_PUBLISH_ROOT": "live"}, "MENTION_WS_PUBLISH_ROOT must not start"},
		{"ws publish root equals the timer root", map[string]string{"MENTION_WS_PUBLISH_ROOT": "timer"}, "MENTION_WS_PUBLISH_ROOT must not start"},
		{"ws publish root sharing only a prefix", map[string]string{"MENTION_WS_PUBLISH_ROOT": "events.publish"}, ""},
		{"phase 2 notify root", map[string]string{"MENTION_WS_PUBLISH_ROOT": "notify", "MENTION_WS_PUBLISH_STREAM": "CHATIM_NOTIFY"}, ""},
		{"mention fetch wait above the projection wait", map[string]string{"MENTION_FETCH_WAIT": "3001ms"}, "MENTION_FETCH_WAIT must not exceed PROJECTION_FETCH_WAIT"},
		{"mention fetch wait at the projection wait", map[string]string{"MENTION_FETCH_WAIT": "3s"}, ""},
		{"notify age above the duplicate window", map[string]string{"MENTION_NOTIFY_MAX_AGE": "7m"}, "MENTION_*"},
		{"bad ws publish stream name", map[string]string{"MENTION_WS_PUBLISH_STREAM": "chatim_ws"}, "MENTION_*"},
		{"bad ws publish root", map[string]string{"MENTION_WS_PUBLISH_ROOT": "websocket.*"}, "MENTION_*"},
		{"targets page over the max", map[string]string{"MENTION_NOTIFY_TARGETS_PAGE": "10001"}, "MENTION_*"},
		{"ack pending below the fetch batch", map[string]string{"MENTION_MAX_ACK_PENDING": "100", "MENTION_FETCH_BATCH": "200"}, "MENTION_*"},
		{"bad enabled flag", map[string]string{"MENTION_ENABLED": "maybe"}, "MENTION_ENABLED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			_, err := config.Load()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Load() = %v, want no error", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Load() = %v, want error mentioning %q", err, tt.wantErr)
			}
		})
	}
}
