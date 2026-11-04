package exchange_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/exchange"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func assertOneLogLine(t *testing.T, h *harness, want ...string) {
	t.Helper()
	logs := h.logs.String()
	if n := strings.Count(logs, "\n"); n != 1 {
		t.Fatalf("log has %d lines %q; want one", n, logs)
	}
	for _, w := range append(want, "ip="+clientIP) {
		if !strings.Contains(logs, w) {
			t.Fatalf("log %q lacks %q", logs, w)
		}
	}
}

func TestStoreErrorIsUnavailableAndLoggedOnce(t *testing.T) {
	h := newHarness(t)
	h.profiles.err = errStore
	_, err := h.exchange(t, "tenant-token")
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("Exchange error = %v; want unavailable", err)
	}
	assertCalls(t, h, "ready", "verify", "lookup", "sign", "write")
	assertWrites(t, h, map[exchange.WriteResult]uint64{exchange.WriteError: 1})
	if h.stats.StoreErrors() != 1 {
		t.Fatalf("store errors = %d; want 1", h.stats.StoreErrors())
	}
	assertOneLogLine(t, h, "level=WARN", "token exchange unavailable", errStore.Error())
}

func TestDisabledUserIsRefusedBeforeAnyWrite(t *testing.T) {
	for _, state := range []store.State{store.StateDisabled, 0, 3} {
		h := newHarness(t)
		h.cache("ha", "Hà", "", state)
		h.verifier.subject.Name, h.verifier.subject.HasName = "Hà Nguyễn", true
		_, err := h.exchange(t, "tenant-token")
		if !errors.Is(err, exchange.ErrDisabled) || errors.Is(err, exchange.ErrRefused) {
			t.Fatalf("state %d: Exchange error = %v; want ErrDisabled", state, err)
		}
		assertCalls(t, h, "ready", "verify", "lookup")
		assertOneLogLine(t, h, "level=INFO", "reason=user_disabled", "iss="+acmeIssuer, "kid="+acmeKeyID)
	}
}

func TestDisabledTenantIsDisabled(t *testing.T) {
	h := newHarness(t)
	h.verifier.err = trustRefusal(t, store.StateDisabled, "chatim", trust.ReasonTenantDisabled)
	_, err := h.exchange(t, "tenant-token")
	if !errors.Is(err, exchange.ErrDisabled) || errors.Is(err, exchange.ErrRefused) {
		t.Fatalf("Exchange error = %v; want ErrDisabled", err)
	}
	assertCalls(t, h, "ready", "verify")
	assertOneLogLine(t, h, "reason=tenant_disabled")
}

func TestRefusedTokenTouchesNothingAndIsLoggedOnceWithoutTheToken(t *testing.T) {
	h := newHarness(t)
	h.verifier.err = trustRefusal(t, store.StateActive, "someone-else", trust.ReasonAudience)
	token := unsignedToken(t, map[string]any{"alg": "EdDSA", "kid": acmeKeyID}, map[string]any{"iss": acmeIssuer, "sub": "ha"})
	_, err := h.exchange(t, token)
	if !errors.Is(err, exchange.ErrRefused) || errors.Is(err, exchange.ErrDisabled) {
		t.Fatalf("Exchange error = %v; want ErrRefused", err)
	}
	assertCalls(t, h, "ready", "verify")
	assertWrites(t, h, nil)
	assertOneLogLine(t, h, "level=INFO", "reason=audience", "iss="+acmeIssuer, "kid="+acmeKeyID)
	if logs := h.logs.String(); strings.Contains(logs, token) || strings.Contains(logs, strings.Split(token, ".")[1]) {
		t.Fatalf("log %q holds the token", logs)
	}
}

func TestUnavailableDependenciesAreLoggedOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cached bool
		setup  func(*harness)
		calls  []string
	}{
		{name: "directory not ready", cached: true, setup: func(h *harness) { h.dir.notReady = true }, calls: []string{"ready"}},
		{name: "trust not loaded", cached: true, setup: func(h *harness) { h.verifier.err = trust.ErrNotReady }, calls: []string{"ready", "verify"}},
		{name: "no signing key", cached: true, setup: func(h *harness) { h.signers.signer = nil }, calls: []string{"ready", "verify", "lookup", "sign"}},
		{name: "no signing key writes no new user", setup: func(h *harness) { h.signers.signer = nil }, calls: []string{"ready", "verify", "lookup", "sign"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if tc.cached {
				h.cache("ha", "Hà", "", store.StateActive)
			}
			tc.setup(h)
			_, err := h.exchange(t, "tenant-token")
			if !errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, exchange.ErrRefused) {
				t.Fatalf("Exchange error = %v; want unavailable", err)
			}
			assertCalls(t, h, tc.calls...)
			assertWrites(t, h, nil)
			assertOneLogLine(t, h, "level=WARN", "token exchange unavailable")
		})
	}
}

func TestCanceledClientIsNotCountedAsAStoreError(t *testing.T) {
	h := newHarness(t)
	h.profiles.err = context.Canceled
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := h.svc.Exchange(ctx, "tenant-token", clientIP)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("Exchange error = %v; want canceled and unavailable", err)
	}
	assertWrites(t, h, map[exchange.WriteResult]uint64{})
	if h.stats.StoreErrors() != 0 {
		t.Fatalf("store errors = %d; want 0 for a client that left", h.stats.StoreErrors())
	}
	if h.logs.Len() != 0 {
		t.Fatalf("log %q; want nothing for a client that left", h.logs.String())
	}
}
