package exchange_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/exchange"
	"github.com/ivannguyendev/chatim/apps/identity/internal/signing"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
)

func assertCalls(t *testing.T, h *harness, want ...string) {
	t.Helper()
	if got := h.rec.list(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v; want %v", got, want)
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
	issued, err := h.exchange(t, "tenant-token")
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
	assertWrites(t, h, nil)
	if h.logs.Len() != 0 {
		t.Fatalf("a granted exchange logged %q; want nothing", h.logs.String())
	}
}

func TestTwoExchangesGetTwoSessions(t *testing.T) {
	h := newHarness(t)
	h.cache("ha", "Hà", "", store.StateActive)
	first, err := h.exchange(t, "tenant-token")
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.exchange(t, "tenant-token")
	if err != nil {
		t.Fatal(err)
	}
	if a, b := h.parse(t, first.AccessToken).Session, h.parse(t, second.AccessToken).Session; a == b {
		t.Fatalf("both exchanges got sid %q; want two sessions", a)
	}
}

func TestNilRandomFallsBackToCryptoRand(t *testing.T) {
	h := newHarness(t, func(d *exchange.Deps) { d.Random = nil })
	h.cache("ha", "Hà", "", store.StateActive)
	issued, err := h.exchange(t, "tenant-token")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if sid := h.parse(t, issued.AccessToken).Session; !signing.ValidSessionID(sid) {
		t.Fatalf("sid = %q; want a valid session id", sid)
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
		{name: "empty picture keeps the cached avatar", cached: true,
			claim: func(s *trust.Subject) { s.Picture, s.HasPicture = "", true }},
		{name: "new name updates from the cached profile", cached: true,
			claim: func(s *trust.Subject) { s.Name, s.HasName = "Hà Nguyễn", true },
			want: &store.ProfileWrite{DisplayName: "Hà Nguyễn", AvatarURL: "https://cdn.acme/ha.png",
				Seen: &store.SeenProfile{DisplayName: "Hà", AvatarURL: "https://cdn.acme/ha.png"}},
			writeRes: exchange.WriteUpdated},
		{name: "new name with an empty picture keeps the cached avatar", cached: true,
			claim: func(s *trust.Subject) { s.Name, s.HasName, s.Picture, s.HasPicture = "Hà Nguyễn", true, "", true },
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
			if _, err := h.exchange(t, "tenant-token"); err != nil {
				t.Fatalf("Exchange: %v", err)
			}
			if tc.want == nil {
				assertCalls(t, h, "ready", "verify", "lookup", "sign")
				assertWrites(t, h, nil)
				return
			}
			assertCalls(t, h, "ready", "verify", "lookup", "sign", "write")
			want := *tc.want
			want.TenantID, want.UserID, want.At, want.By = "acme", "ha", testNow, "jit:"+acmeIssuer
			if got := h.profiles.writes[0]; !profileEqual(got, want) {
				t.Fatalf("write = %+v (seen %+v); want %+v (seen %+v)", got, got.Seen, want, want.Seen)
			}
			assertWrites(t, h, map[exchange.WriteResult]uint64{tc.writeRes: 1})
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
	issued, err := h.exchange(t, "tenant-token")
	if err != nil || issued.AccessToken == "" {
		t.Fatalf("Exchange = %+v, %v; want a token", issued, err)
	}
	assertWrites(t, h, map[exchange.WriteResult]uint64{exchange.WriteConflict: 1})
}
