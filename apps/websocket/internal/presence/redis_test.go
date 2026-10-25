package presence

import (
	"errors"
	"testing"
	"time"
)

func TestRedisOptionsFollowThePresenceContract(t *testing.T) {
	got := RedisOptions(RedisConfig{Addr: "chatim-redis-presence:6379", DB: 2, Password: testPassword, Pool: 64, Timeout: testTimeout, Node: "websocket-1"})
	if got.Addr != "chatim-redis-presence:6379" || got.DB != 2 || got.Password != testPassword || got.PoolSize != 64 ||
		got.ClientName != "chatim-websocket-presence-websocket-1" {
		t.Fatalf("options = %+v", got)
	}
	if got.DialTimeout != testTimeout || got.ReadTimeout != testTimeout || got.WriteTimeout != testTimeout {
		t.Fatalf("timeouts dial %v read %v write %v, want %v", got.DialTimeout, got.ReadTimeout, got.WriteTimeout, testTimeout)
	}
	if got.MaxRetries != 1 || got.DialerRetries != 1 || !got.DisableIdentity || !got.ContextTimeoutEnabled {
		t.Fatalf("retries %d dialer %d identity off %v context timeouts %v", got.MaxRetries, got.DialerRetries, got.DisableIdentity, got.ContextTimeoutEnabled)
	}
}

func TestNewClientNeedsAConnectionAndATimeout(t *testing.T) {
	if _, err := NewClient(nil, time.Second); !errors.Is(err, errNoConn) {
		t.Fatalf("NewClient(nil) = %v", err)
	}
	client := newClientTo(t, "127.0.0.1:1")
	defer func() { _ = client.Close() }()
	if _, err := NewClient(client.conn, 0); !errors.Is(err, errNoTimeout) {
		t.Fatalf("NewClient(timeout 0) = %v", err)
	}
}
