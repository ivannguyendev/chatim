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
	setEnv(t, map[string]string{"MENTION_ENABLED": "false", "NOTIFY_STREAM": "CHATIM_EVT", "MENTION_FETCH_WAIT": "10s"})
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
		{"notify stream named like the event stream", map[string]string{"NOTIFY_STREAM": "CHATIM_EVT"}, "NOTIFY_STREAM must differ"},
		{"notify stream named like the timer stream", map[string]string{"NOTIFY_STREAM": "CHATIM_TIMER"}, "NOTIFY_STREAM must differ"},
		{"notify root equals the event root", map[string]string{"NOTIFY_SUBJECT_ROOT": "evt"}, "NOTIFY_SUBJECT_ROOT must differ"},
		{"notify root equals the live root", map[string]string{"NOTIFY_SUBJECT_ROOT": "live"}, "NOTIFY_SUBJECT_ROOT must differ"},
		{"notify root equals the timer root", map[string]string{"NOTIFY_SUBJECT_ROOT": "timer"}, "NOTIFY_SUBJECT_ROOT must differ"},
		{"mention fetch wait above the projection wait", map[string]string{"MENTION_FETCH_WAIT": "3001ms"}, "MENTION_FETCH_WAIT must not exceed PROJECTION_FETCH_WAIT"},
		{"mention fetch wait at the projection wait", map[string]string{"MENTION_FETCH_WAIT": "3s"}, ""},
		{"notify age above the duplicate window", map[string]string{"MENTION_NOTIFY_MAX_AGE": "7m"}, "MENTION_*, NOTIFY_*"},
		{"bad notify stream name", map[string]string{"NOTIFY_STREAM": "chatim_notify"}, "MENTION_*, NOTIFY_*"},
		{"ack pending below the fetch batch", map[string]string{"MENTION_MAX_ACK_PENDING": "100", "MENTION_FETCH_BATCH": "200"}, "MENTION_*, NOTIFY_*"},
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
