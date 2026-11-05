package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/config"
)

func TestJITWriterDefaults(t *testing.T) {
	setRequired(t)
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.JITWriters != 4 || c.JITWindow != 2*time.Millisecond || c.JITMaxBatch != 256 || c.JITQueue != 1024 || c.JITInsertTimeout != time.Second {
		t.Fatalf("jit defaults = %d %v %d %d %v", c.JITWriters, c.JITWindow, c.JITMaxBatch, c.JITQueue, c.JITInsertTimeout)
	}
}

func TestJITWriterSettingsAreRead(t *testing.T) {
	setRequired(t)
	t.Setenv("IDENTITY_JIT_WRITERS", "8")
	t.Setenv("IDENTITY_JIT_WINDOW", "5ms")
	t.Setenv("IDENTITY_JIT_MAX_BATCH", "64")
	t.Setenv("IDENTITY_JIT_QUEUE", "512")
	t.Setenv("IDENTITY_JIT_INSERT_TIMEOUT", "3s")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.JITWriters != 8 || c.JITWindow != 5*time.Millisecond || c.JITMaxBatch != 64 || c.JITQueue != 512 || c.JITInsertTimeout != 3*time.Second {
		t.Fatalf("jit settings = %d %v %d %d %v", c.JITWriters, c.JITWindow, c.JITMaxBatch, c.JITQueue, c.JITInsertTimeout)
	}
	out := c.String()
	for _, key := range []string{"jit_writers=8", "jit_window=5ms", "jit_max_batch=64", "jit_queue=512", "jit_insert_timeout=3s"} {
		if !strings.Contains(out, key) {
			t.Fatalf("config log lacks %s: %s", key, out)
		}
	}
}

func TestLoadRefusesNonPositiveJITWriterSettings(t *testing.T) {
	cases := map[string]struct {
		key, value string
	}{
		"zero writers":           {"IDENTITY_JIT_WRITERS", "0"},
		"zero window":            {"IDENTITY_JIT_WINDOW", "0s"},
		"negative max batch":     {"IDENTITY_JIT_MAX_BATCH", "-1"},
		"zero queue":             {"IDENTITY_JIT_QUEUE", "0"},
		"negative insert budget": {"IDENTITY_JIT_INSERT_TIMEOUT", "-1s"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tc.key, tc.value)
			want := tc.key + " must be positive"
			if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Load = %v, want an error containing %q", err, want)
			}
		})
	}
}
