package dedupe

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNewAppliesDefaults(t *testing.T) {
	_, rdb := newRedis(t)
	s, err := New(rdb, Config{CoreID: "core-a"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := Config{CoreID: "core-a", PendingTTL: 10 * time.Second, CommittedTTL: 15 * time.Minute, Timeout: 100 * time.Millisecond, Cooldown: time.Second}
	if s.cfg != want {
		t.Fatalf("config = %+v, want %+v", s.cfg, want)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	_, rdb := newRedis(t)
	base := Config{CoreID: "core-a"}
	mutations := map[string]func(*Config){
		"empty core id":           func(c *Config) { c.CoreID = "" },
		"core id with colon":      func(c *Config) { c.CoreID = "core:a" },
		"core id with space":      func(c *Config) { c.CoreID = "core a" },
		"core id with control":    func(c *Config) { c.CoreID = "core\x01" },
		"core id too long":        func(c *Config) { c.CoreID = strings.Repeat("a", maxCoreIDLen+1) },
		"negative timeout":        func(c *Config) { c.Timeout = -time.Millisecond },
		"negative cooldown":       func(c *Config) { c.Cooldown = -time.Second },
		"sub-millisecond pending": func(c *Config) { c.PendingTTL, c.Timeout = 500*time.Microsecond, 100*time.Microsecond },
		"pending within timeout":  func(c *Config) { c.PendingTTL, c.Timeout = 100*time.Millisecond, 100*time.Millisecond },
		"committed below pending": func(c *Config) { c.CommittedTTL = 5 * time.Second },
		"negative committed":      func(c *Config) { c.CommittedTTL = -time.Minute },
		"negative pending":        func(c *Config) { c.PendingTTL = -time.Second },
	}
	for name, mutate := range mutations {
		cfg := base
		mutate(&cfg)
		if _, err := New(rdb, cfg, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestNewRejectsClientsThatIgnoreContextDeadlines(t *testing.T) {
	if _, err := New(nil, Config{CoreID: "core-a"}, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil client) = %v, want ErrInvalidArgument", err)
	}
	mr, _ := newRedis(t)
	plain := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = plain.Close() })
	if _, err := New(plain, Config{CoreID: "core-a"}, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(client without context timeouts) = %v, want ErrInvalidArgument", err)
	}
}
