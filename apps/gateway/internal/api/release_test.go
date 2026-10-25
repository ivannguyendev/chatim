package api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func awaitServed(t *testing.T, h *harness) {
	t.Helper()
	select {
	case <-h.served:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler did not return")
	}
}

func drainServed(h *harness) {
	for {
		select {
		case <-h.served:
		default:
			return
		}
	}
}

func historyRequest(ctx context.Context, h *harness, token string) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.server.URL+historyPath, bytes.NewReader(historyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	return req
}

func assertSlotsFree(t *testing.T, h *harness, token string) {
	t.Helper()
	if got := h.read(t, "inflight", nil); got != 0 {
		t.Fatalf("node inflight = %v, want 0", got)
	}
	if resp := h.post(t, historyPath, "application/json", token, historyJSON, nil); resp.status != http.StatusOK {
		t.Fatalf("next call status = %d: the user's slot was not released", resp.status)
	}
}

func TestAHandlerPanicReleasesTheGateAndTheUserSlot(t *testing.T) {
	h := newHarness(t, limits{rate: 100, burst: 100, inflight: 1, node: 1})
	token := bearerOf(h.token(t, claims{}))
	h.panics.Store(true)
	if resp, err := h.server.Client().Do(historyRequest(t.Context(), h, token)); err == nil {
		_ = resp.Body.Close()
	}
	awaitServed(t, h)
	h.panics.Store(false)
	assertSlotsFree(t, h, token)
}

func TestAClientCancelReleasesTheGateAndTheUserSlot(t *testing.T) {
	h := newHarness(t, limits{rate: 100, burst: 100, inflight: 1, node: 1})
	started, release := hangCore(h)
	token := bearerOf(h.token(t, claims{}))
	ctx, cancel := context.WithCancel(t.Context())
	failed := make(chan error, 1)
	go func() {
		resp, err := h.server.Client().Do(historyRequest(ctx, h, token))
		if err == nil {
			_ = resp.Body.Close()
		}
		failed <- err
	}()
	<-started
	cancel()
	if err := <-failed; err == nil {
		t.Fatal("the cancelled call succeeded")
	}
	awaitServed(t, h)
	close(release)
	assertSlotsFree(t, h, token)
}

func TestGetStreamingAndUnknownPathsWithoutTokenGet401(t *testing.T) {
	h := newHarness(t, roomy)
	query := url.Values{"connect": {"v1"}, "encoding": {"json"}, "message": {string(historyJSON)}}
	get, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.server.URL+historyPath+"?"+query.Encode(), http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if resp := h.do(t, get); resp.status != http.StatusUnauthorized || errorCode(t, resp) != "unauthenticated" {
		t.Fatalf("GET status = %d", resp.status)
	}
	for _, contentType := range []string{"application/connect+json", "application/grpc", "application/grpc-web+proto"} {
		if resp := h.post(t, historyPath, contentType, "", historyJSON, nil); resp.status != http.StatusUnauthorized {
			t.Fatalf("%s status = %d", contentType, resp.status)
		}
	}
	if resp := h.post(t, "/chatim.v1.CoreService/Nope", "application/json", "", nil, nil); resp.status != http.StatusUnauthorized {
		t.Fatalf("unknown path status = %d", resp.status)
	}
	if got := h.read(t, "commands_total", map[string]string{"command": "unknown", "code": "Unimplemented"}); got != 0 {
		t.Fatalf("an unauthenticated unknown path was counted: %v", got)
	}
	if got := h.read(t, "auth_failures_total", map[string]string{"reason": "missing"}); got != 5 {
		t.Fatalf("missing tokens counted = %v", got)
	}
	drainServed(h)
	if len(h.core.recorded()) != 0 {
		t.Fatal("an unauthenticated request reached core")
	}
}
