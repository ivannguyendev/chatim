package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/auth/authtest"
)

type unreachableIdentity struct{}

func (unreachableIdentity) Do(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, errors.New("identity unreachable")
}

func TestReadyWaitsForTheFirstIdentitySyncOrItsTimeout(t *testing.T) {
	keys := newSigner(t)
	core := startCore(t, &fakeCore{})
	lis, logs := newListeners(), newLogWatch()
	cfg := testConfig(keys.path, core)
	cfg.Identity.ReadyTimeout = 2 * time.Second
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := time.Now()
	done := runAsyncWith(ctx, cfg, logs.logger(), Deps{Listen: lis.listen, IdentityDoer: unreachableIdentity{}})
	adminAddr := receive(t, lis.admin, "admin listener")
	receive(t, lis.api, "api listener")
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d before the first identity sync, want 503", code)
	}
	logs.await(t, "gateway ready")
	if waited := time.Since(started); waited < time.Second || waited > 4*time.Second {
		t.Fatalf("ready after %v; want about the 2s ready timeout", waited)
	}
	_, body := httpStatus(t, adminAddr, "/metrics")
	for _, want := range []string{`chatim_gateway_identity_refused{kind="revocations"} 0`, "chatim_gateway_identity_boot_timeouts_total 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	cancel()
	if err := receive(t, done, "Run return"); err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestNodeRefusedByIdentityStaysNotReady(t *testing.T) {
	keys := newSigner(t)
	core := startCore(t, &fakeCore{})
	lis, logs := newListeners(), newLogWatch()
	cfg := testConfig(keys.path, core)
	cfg.Identity.ReadyTimeout = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runAsyncWith(ctx, cfg, logs.logger(), Deps{Listen: lis.listen, IdentityDoer: authtest.Refusing{Status: http.StatusNotFound}})
	adminAddr := receive(t, lis.admin, "admin listener")
	logs.await(t, "identity refused this node")
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d while identity refuses the node, want 503", code)
	}
	if _, body := httpStatus(t, adminAddr, "/metrics"); !strings.Contains(body, `chatim_gateway_identity_refused{kind="keys"} 404`) {
		t.Fatalf("/metrics lacks the refusal: %s", body)
	}
	cancel()
	if err := receive(t, done, "Run return"); err != nil {
		t.Fatalf("Run = %v", err)
	}
}
