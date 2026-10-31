package iamsync_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/iamsync"
)

func TestRedisSharedRoundTrip(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	shared := iamsync.NewRedisShared(client, "chatim-dev")
	other := iamsync.NewRedisShared(client, "chatim-staging")
	ctx := context.Background()
	if got, err := shared.Get(ctx); err != nil || got != nil {
		t.Fatalf("Get on empty = %q, %v; want nil, nil", got, err)
	}
	if err := shared.Put(ctx, []byte(`{"x":1}`), time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got, err := shared.Get(ctx); err != nil || string(got) != `{"x":1}` {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if ttl := mr.TTL(iamsync.SharedKey + ":chatim-dev"); ttl != time.Minute {
		t.Fatalf("ttl = %v", ttl)
	}
	if got, err := other.Get(ctx); err != nil || got != nil {
		t.Fatalf("another issuer read %q, %v; want nil, nil", got, err)
	}
	mr.Close()
	if _, err := shared.Get(ctx); err == nil {
		t.Fatal("Get on a closed redis returned no error")
	}
}
