package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/iamsync/iamsynctest"
)

type blockedIAM struct {
	release chan struct{}
	calls   atomic.Int64
}

func (b *blockedIAM) Do(req *http.Request) (*http.Response, error) {
	b.calls.Add(1)
	select {
	case <-b.release:
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte(`{"sessions":[],"tenants":[]}`)))}, nil
	case <-req.Context().Done():
		return nil, errors.New("iam unreachable")
	}
}

func TestReadyWaitsForTheFirstIAMSyncOrItsTimeout(t *testing.T) {
	mr := slotRedis(t)
	keys := newSigner(t)
	_, dial := startFakeCore(t)
	lis, logs := newListeners(), newLogWatch()
	cfg := testConfig(mr.Addr(), keys.path)
	cfg.IAM.ReadyTimeout, cfg.IAM.HTTPTimeout = 2*time.Second, 5*time.Second
	iam := &blockedIAM{release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	done := runAsync(ctx, cfg, logs.logger(), Deps{PresenceRedis: newPresenceRedis(t).open, Dial: dial, Listen: lis.listen, NATS: newFakeNATS().dial, IAMDoer: iam})
	adminAddr := awaitListener(t, lis.admin)
	awaitListener(t, lis.ws)
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d before the first iam sync, want 503", code)
	}
	logs.await(t, "websocket ready")
	if waited := time.Since(started); waited > 4*time.Second {
		t.Fatalf("ready after %v; want about the 2s ready timeout, not the 5s http timeout", waited)
	}
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz = %d after the ready timeout, want 200", code)
	}
	_, body := httpStatus(t, adminAddr, "/metrics")
	for _, want := range []string{`chatim_websocket_iam_refused{kind="revocations"} 0`, "chatim_websocket_iam_boot_timeouts_total 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestNodeRefusedByIAMStaysNotReady(t *testing.T) {
	mr := slotRedis(t)
	keys := newSigner(t)
	_, dial := startFakeCore(t)
	lis, logs := newListeners(), newLogWatch()
	cfg := testConfig(mr.Addr(), keys.path)
	cfg.IAM.ReadyTimeout = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(ctx, cfg, logs.logger(), Deps{PresenceRedis: newPresenceRedis(t).open, Dial: dial, Listen: lis.listen, NATS: newFakeNATS().dial, IAMDoer: iamsynctest.Refusing{Status: http.StatusUnauthorized}})
	adminAddr := awaitListener(t, lis.admin)
	logs.await(t, "iam refused this node")
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d while iam refuses the node, want 503", code)
	}
	if _, body := httpStatus(t, adminAddr, "/metrics"); !strings.Contains(body, `chatim_websocket_iam_refused{kind="revocations"} 401`) {
		t.Fatalf("/metrics lacks the refusal: %s", body)
	}
	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run = %v", err)
	}
}
