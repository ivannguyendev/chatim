package exchange_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/exchange"
	"github.com/ivannguyendev/chatim/apps/identity/internal/signing"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func assertCalls(t *testing.T, h *harness, want ...string) {
	t.Helper()
	if got := h.rec.list(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v; want %v", got, want)
	}
}

func assertResult(t *testing.T, h *harness, want exchange.Result) {
	t.Helper()
	for _, r := range exchange.Results {
		n := uint64(0)
		if r == want {
			n = 1
		}
		if got := h.stats.Exchanges(r); got != n {
			t.Fatalf("exchanges{%s} = %d; want %d", r, got, n)
		}
	}
}

func assertWrites(t *testing.T, h *harness, want map[exchange.WriteResult]uint64) {
	t.Helper()
	for _, r := range exchange.WriteResults {
		if got := h.stats.Writes(r); got != want[r] {
			t.Fatalf("jit writes{%s} = %d; want %d", r, got, want[r])
		}
	}
}

func TestKnownUserGetsAChatToken(t *testing.T) {
	h := newHarness(t)
	h.cache("ha", "Hà", "", store.StateActive)
	issued, err := h.svc.Exchange(t.Context(), "tenant-token")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	c := h.parse(t, issued.AccessToken)
	if c.Tenant != "acme" || c.Subject != "ha" || c.Issuer != chatIssuer || !signing.ValidSessionID(c.Session) {
		t.Fatalf("claims tid=%q sub=%q iss=%q sid=%q; want acme, ha, %s and a valid sid", c.Tenant, c.Subject, c.Issuer, c.Session, chatIssuer)
	}
	if !c.IssuedAt.Equal(testNow) || c.ExpiresAt.Sub(c.IssuedAt.Time) != tokenTTL || !issued.ExpiresAt.Equal(c.ExpiresAt.Time) {
		t.Fatalf("iat=%v exp=%v ExpiresAt=%v; want iat now and exp-iat = %v", c.IssuedAt, c.ExpiresAt, issued.ExpiresAt, tokenTTL)
	}
	assertCalls(t, h, "ready", "verify", "lookup", "sign")
	assertResult(t, h, exchange.ResultOK)
	assertWrites(t, h, nil)
}

func TestTwoExchangesGetTwoSessions(t *testing.T) {
	h := newHarness(t)
	h.cache("ha", "Hà", "", store.StateActive)
	first, err := h.svc.Exchange(t.Context(), "tenant-token")
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.svc.Exchange(t.Context(), "tenant-token")
	if err != nil {
		t.Fatal(err)
	}
	if a, b := h.parse(t, first.AccessToken).Session, h.parse(t, second.AccessToken).Session; a == b {
		t.Fatalf("both exchanges got sid %q; want two sessions", a)
	}
}

func TestJustInTimeProfileWrites(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cached   bool
		claim    func(*trust.Subject)
		want     *store.ProfileWrite
		writeRes exchange.WriteResult
	}{
		{name: "same name and picture as the cache writes nothing", cached: true,
			claim: func(s *trust.Subject) {
				s.Name, s.HasName, s.Picture, s.HasPicture = " Hà ", true, "https://cdn.acme/ha.png", true
			}},
		{name: "invalid name and http picture keep the cache", cached: true,
			claim: func(s *trust.Subject) {
				s.Name, s.HasName, s.Picture, s.HasPicture = "\u200e ", true, "http://cdn.acme/x.png", true
			}},
		{name: "new name updates from the cached profile", cached: true,
			claim: func(s *trust.Subject) { s.Name, s.HasName = "Hà Nguyễn", true },
			want: &store.ProfileWrite{DisplayName: "Hà Nguyễn", AvatarURL: "https://cdn.acme/ha.png",
				Seen: &store.SeenProfile{DisplayName: "Hà", AvatarURL: "https://cdn.acme/ha.png"}},
			writeRes: exchange.WriteUpdated},
		{name: "new picture alone updates", cached: true,
			claim: func(s *trust.Subject) { s.Picture, s.HasPicture = "https://cdn.acme/new.png", true },
			want: &store.ProfileWrite{DisplayName: "Hà", AvatarURL: "https://cdn.acme/new.png",
				Seen: &store.SeenProfile{DisplayName: "Hà", AvatarURL: "https://cdn.acme/ha.png"}},
			writeRes: exchange.WriteUpdated},
		{name: "new user takes the normalized name and picture",
			claim: func(s *trust.Subject) {
				s.Name, s.HasName, s.Picture, s.HasPicture = " Hà\u200f ", true, "https://cdn.acme/ha.png", true
			},
			want: &store.ProfileWrite{DisplayName: "Hà", AvatarURL: "https://cdn.acme/ha.png"}, writeRes: exchange.WriteCreated},
		{name: "new user without a name is named by sub", claim: func(*trust.Subject) {},
			want: &store.ProfileWrite{DisplayName: "ha"}, writeRes: exchange.WriteCreated},
		{name: "new user with an invalid name and http picture",
			claim: func(s *trust.Subject) {
				s.Name, s.HasName, s.Picture, s.HasPicture = "   ", true, "http://cdn.acme/ha.png", true
			},
			want: &store.ProfileWrite{DisplayName: "ha"}, writeRes: exchange.WriteCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if tc.cached {
				h.cache("ha", "Hà", "https://cdn.acme/ha.png", store.StateActive)
			}
			tc.claim(&h.verifier.subject)
			if _, err := h.svc.Exchange(t.Context(), "tenant-token"); err != nil {
				t.Fatalf("Exchange: %v", err)
			}
			if tc.want == nil {
				assertCalls(t, h, "ready", "verify", "lookup", "sign")
				assertWrites(t, h, nil)
				return
			}
			assertCalls(t, h, "ready", "verify", "lookup", "write", "sign")
			want := *tc.want
			want.TenantID, want.UserID, want.At, want.By = "acme", "ha", testNow, "jit:"+acmeIssuer
			if got := h.profiles.writes[0]; !profileEqual(got, want) {
				t.Fatalf("write = %+v (seen %+v); want %+v (seen %+v)", got, got.Seen, want, want.Seen)
			}
			assertWrites(t, h, map[exchange.WriteResult]uint64{tc.writeRes: 1})
			assertResult(t, h, exchange.ResultOK)
		})
	}
}

func profileEqual(a, b store.ProfileWrite) bool {
	if (a.Seen == nil) != (b.Seen == nil) || (a.Seen != nil && *a.Seen != *b.Seen) {
		return false
	}
	a.Seen, b.Seen = nil, nil
	return a == b
}

func TestLostProfileRaceStillIssues(t *testing.T) {
	h := newHarness(t)
	h.verifier.subject.Name, h.verifier.subject.HasName = "Hà", true
	h.profiles.written = false
	issued, err := h.svc.Exchange(t.Context(), "tenant-token")
	if err != nil || issued.AccessToken == "" {
		t.Fatalf("Exchange = %+v, %v; want a token", issued, err)
	}
	assertWrites(t, h, map[exchange.WriteResult]uint64{exchange.WriteConflict: 1})
	assertResult(t, h, exchange.ResultOK)
}

func TestStoreErrorIsUnavailableAndSignsNothing(t *testing.T) {
	h := newHarness(t)
	h.profiles.err = errStore
	_, err := h.svc.Exchange(t.Context(), "tenant-token")
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("Exchange error = %v; want unavailable", err)
	}
	assertCalls(t, h, "ready", "verify", "lookup", "write")
	assertWrites(t, h, map[exchange.WriteResult]uint64{exchange.WriteError: 1})
	if h.stats.StoreErrors() != 1 {
		t.Fatalf("store errors = %d; want 1", h.stats.StoreErrors())
	}
	assertResult(t, h, exchange.ResultError)
}

func TestDisabledUserIsRefusedBeforeAnyWrite(t *testing.T) {
	h := newHarness(t)
	h.cache("ha", "Hà", "", store.StateDisabled)
	h.verifier.subject.Name, h.verifier.subject.HasName = "Hà Nguyễn", true
	_, err := h.svc.Exchange(t.Context(), "tenant-token")
	if !errors.Is(err, exchange.ErrDisabled) || errors.Is(err, exchange.ErrRefused) {
		t.Fatalf("Exchange error = %v; want ErrDisabled", err)
	}
	assertCalls(t, h, "ready", "verify", "lookup")
	assertResult(t, h, exchange.ResultDisabled)
}

func TestDisabledTenantIsDisabled(t *testing.T) {
	h := newHarness(t)
	h.verifier.err = trustRefusal(t, store.StateDisabled, "chatim", trust.ReasonTenantDisabled)
	_, err := h.svc.Exchange(t.Context(), "tenant-token")
	if !errors.Is(err, exchange.ErrDisabled) || errors.Is(err, exchange.ErrRefused) {
		t.Fatalf("Exchange error = %v; want ErrDisabled", err)
	}
	assertCalls(t, h, "ready", "verify")
	assertResult(t, h, exchange.ResultDisabled)
}

func TestRefusedTokenTouchesNothingAndIsLoggedWithoutTheToken(t *testing.T) {
	h := newHarness(t)
	h.verifier.err = trustRefusal(t, store.StateActive, "someone-else", trust.ReasonAudience)
	token := unsignedToken(t, map[string]any{"alg": "EdDSA", "kid": acmeKeyID}, map[string]any{"iss": acmeIssuer, "sub": "ha"})
	_, err := h.svc.Exchange(t.Context(), token)
	if !errors.Is(err, exchange.ErrRefused) || errors.Is(err, exchange.ErrDisabled) {
		t.Fatalf("Exchange error = %v; want ErrRefused", err)
	}
	assertCalls(t, h, "ready", "verify")
	assertResult(t, h, exchange.ResultInvalid)
	assertWrites(t, h, nil)
	logs := h.logs.String()
	for _, want := range []string{"level=INFO", "reason=audience", "iss=" + acmeIssuer, "kid=" + acmeKeyID} {
		if !strings.Contains(logs, want) {
			t.Fatalf("log %q lacks %q", logs, want)
		}
	}
	if strings.Contains(logs, token) || strings.Contains(logs, strings.Split(token, ".")[1]) {
		t.Fatalf("log %q holds the token", logs)
	}
}

func TestUnavailableDependencies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*harness)
		calls []string
	}{
		{name: "directory not ready", setup: func(h *harness) { h.dir.notReady = true }, calls: []string{"ready"}},
		{name: "trust not loaded", setup: func(h *harness) { h.verifier.err = trust.ErrNotReady }, calls: []string{"ready", "verify"}},
		{name: "no signing key", setup: func(h *harness) { h.signers.signer = nil }, calls: []string{"ready", "verify", "lookup", "sign"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.cache("ha", "Hà", "", store.StateActive)
			tc.setup(h)
			_, err := h.svc.Exchange(t.Context(), "tenant-token")
			if !errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, exchange.ErrRefused) {
				t.Fatalf("Exchange error = %v; want unavailable", err)
			}
			assertCalls(t, h, tc.calls...)
			assertResult(t, h, exchange.ResultError)
		})
	}
}
