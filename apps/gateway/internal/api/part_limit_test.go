package api_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/ratelimit"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type fakePartLimiter struct {
	mu    sync.Mutex
	keys  []ratelimit.Key
	allow int
	err   error
}

func (f *fakePartLimiter) Allow(_ context.Context, k ratelimit.Key) (ratelimit.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, k)
	if f.err != nil {
		return ratelimit.Decision{Allowed: true}, f.err
	}
	if len(f.keys) > f.allow {
		return ratelimit.Decision{Tier: ratelimit.TierDevice}, nil
	}
	return ratelimit.Decision{Allowed: true}, nil
}

func partURLsBody(t *testing.T) []byte {
	t.Helper()
	b, err := proto.Marshal(&chatimv1.GetPartURLsRequest{Key: "acme/an/20261106/x", PartNumbers: []int32{1}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPartURLsAreLimitedByUserIPAndDevice(t *testing.T) {
	parts := &fakePartLimiter{allow: 1}
	l := roomy
	l.parts = parts
	h := newHarness(t, l)
	token := bearerOf(h.token(t, claims{tenant: "acme", user: "an", session: "phone-1"}))
	first := h.post(t, chatimv1.MediaService_GetPartURLs_FullMethodName, "application/proto", token, partURLsBody(t), nil)
	second := h.post(t, chatimv1.MediaService_GetPartURLs_FullMethodName, "application/proto", token, partURLsBody(t), nil)
	if first.status != http.StatusOK || errorCode(t, second) != "resource_exhausted" {
		t.Fatalf("first %d, second %q", first.status, second.body)
	}
	if len(h.chat.recorded()) != 1 {
		t.Fatalf("chat calls = %d", len(h.chat.recorded()))
	}
	want := ratelimit.Key{Tenant: "acme", User: "an", IP: "127.0.0.1", Device: "phone-1"}
	if parts.keys[0] != want {
		t.Fatalf("key %+v, want %+v", parts.keys[0], want)
	}
	other := h.post(t, chatimv1.ChatService_ListMyRooms_FullMethodName, "application/proto", token, nil, nil)
	if other.status != http.StatusOK || len(parts.keys) != 2 {
		t.Fatalf("other command status %d, limiter calls %d", other.status, len(parts.keys))
	}
}

func TestPartURLsPassWhenTheLimiterStoreFails(t *testing.T) {
	l := roomy
	l.parts = &fakePartLimiter{err: errors.New("redis down")}
	h := newHarness(t, l)
	token := bearerOf(h.token(t, claims{}))
	if r := h.post(t, chatimv1.MediaService_GetPartURLs_FullMethodName, "application/proto", token, partURLsBody(t), nil); r.status != http.StatusOK {
		t.Fatalf("status %d", r.status)
	}
}
