package exchange_test

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/exchange"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

const tenantSub = "0901000000"

var issuedUserID = regexp.MustCompile(`^[a-z2-7]{26}$`)

func assertLinks(t *testing.T, h *harness, want map[exchange.LinkOutcome]uint64) {
	t.Helper()
	for _, o := range exchange.LinkOutcomes {
		if got := h.stats.Links(o); got != want[o] {
			t.Fatalf("user links{%s} = %d; want %d", o, got, want[o])
		}
	}
}

func newUserHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.verifier.subject = subject(tenantSub)
	return h
}

func TestNewUserGetsAnIdentityIssuedID(t *testing.T) {
	h := newUserHarness(t)
	issued, err := h.exchange(t, "tenant-token")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	c := h.parse(t, issued.AccessToken)
	if !issuedUserID.MatchString(c.Subject) || ids.ValidUser(c.Subject) != nil || c.Subject == tenantSub || c.Tenant != "acme" {
		t.Fatalf("claims tid=%q sub=%q; want acme and a 26-char lowercase base32 id", c.Tenant, c.Subject)
	}
	assertCalls(t, h, "ready", "verify", "lookup", "sign", "link")
	want := store.NewUser{TenantID: "acme", Realm: acmeIssuer, Sub: tenantSub, CandidateID: c.Subject, At: testNow, By: "jit:" + acmeIssuer}
	if got := h.profiles.links; len(got) != 1 || got[0] != want {
		t.Fatalf("links = %+v; want %+v", got, want)
	}
	assertLinks(t, h, map[exchange.LinkOutcome]uint64{exchange.LinkCreated: 1})
	assertWrites(t, h, nil)
	if h.logs.Len() != 0 {
		t.Fatalf("a granted exchange logged %q; want nothing", h.logs.String())
	}
}

func TestNewUserProfileComesFromValidClaims(t *testing.T) {
	for name, tc := range map[string]struct {
		claim       func(*trust.Subject)
		name, image string
	}{
		"normalized name and https picture": {
			claim: func(s *trust.Subject) {
				s.Name, s.HasName, s.Picture, s.HasPicture = " Hà\u200f ", true, "https://cdn.acme/ha.png", true
			},
			name: "Hà", image: "https://cdn.acme/ha.png",
		},
		"no name claim leaves the name empty": {claim: func(*trust.Subject) {}},
		"invalid name and http picture": {
			claim: func(s *trust.Subject) {
				s.Name, s.HasName, s.Picture, s.HasPicture = "   ", true, "http://cdn.acme/ha.png", true
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newUserHarness(t)
			tc.claim(&h.verifier.subject)
			if _, err := h.exchange(t, "tenant-token"); err != nil {
				t.Fatalf("Exchange: %v", err)
			}
			if got := h.profiles.links[0]; got.DisplayName != tc.name || got.AvatarURL != tc.image {
				t.Fatalf("new user name=%q avatar=%q; want %q, %q", got.DisplayName, got.AvatarURL, tc.name, tc.image)
			}
		})
	}
}

func TestLinkedUserIDIsTheTokenSubject(t *testing.T) {
	for _, tc := range []struct {
		result store.LinkResult
		want   exchange.LinkOutcome
	}{
		{store.LinkExisting, exchange.LinkExisting},
		{store.LinkHealed, exchange.LinkHealed},
	} {
		t.Run(tc.result.String(), func(t *testing.T) {
			h := newUserHarness(t)
			h.profiles.link = func(store.NewUser) (store.LinkedUser, error) {
				return store.LinkedUser{ID: knownID, Result: tc.result}, nil
			}
			issued, err := h.exchange(t, "tenant-token")
			if err != nil {
				t.Fatalf("Exchange: %v", err)
			}
			if sub := h.parse(t, issued.AccessToken).Subject; sub != knownID {
				t.Fatalf("token sub = %q; want the linked id %q", sub, knownID)
			}
			assertLinks(t, h, map[exchange.LinkOutcome]uint64{tc.want: 1})
		})
	}
}

func TestFailedLinkIssuesNoToken(t *testing.T) {
	errPending := fmt.Errorf("%w: user link not visible yet", apperr.ErrUnavailable)
	for name, tc := range map[string]struct {
		linked      store.LinkedUser
		err         error
		want        exchange.LinkOutcome
		storeErrors uint64
		logs        []string
	}{
		"winner not visible yet":   {linked: store.LinkedUser{Result: store.LinkPending}, err: errPending, want: exchange.LinkPending},
		"pending without an error": {linked: store.LinkedUser{Result: store.LinkPending}, want: exchange.LinkPending},
		"store error":              {err: errStore, want: exchange.LinkError, storeErrors: 1},
		"unknown result":           {linked: store.LinkedUser{ID: knownID}, want: exchange.LinkError, storeErrors: 1},
		"corrupt stored id": {err: store.ErrLinkCorrupt, want: exchange.LinkError, storeErrors: 1,
			logs: []string{"level=WARN", `msg="user link corrupt"`, "tenant=acme", "realm=" + acmeIssuer}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newUserHarness(t)
			h.profiles.link = func(store.NewUser) (store.LinkedUser, error) { return tc.linked, tc.err }
			issued, err := h.exchange(t, "tenant-token")
			if !errors.Is(err, apperr.ErrUnavailable) || issued != (exchange.Issued{}) {
				t.Fatalf("Exchange = %+v, %v; want unavailable and no token", issued, err)
			}
			assertLinks(t, h, map[exchange.LinkOutcome]uint64{tc.want: 1})
			if got := h.stats.StoreErrors(); got != tc.storeErrors {
				t.Fatalf("store errors = %d; want %d", got, tc.storeErrors)
			}
			logs := h.logs.String()
			for _, w := range append(tc.logs, "token exchange unavailable") {
				if !strings.Contains(logs, w) {
					t.Fatalf("log %q lacks %q", logs, w)
				}
			}
			if strings.Contains(logs, tenantSub) {
				t.Fatalf("log %q holds the tenant sub", logs)
			}
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("entropy gone") }

func TestNoRandomnessLinksNoUser(t *testing.T) {
	h := newHarness(t, func(d *exchange.Deps) { d.Random = failingReader{} })
	h.verifier.subject = subject(tenantSub)
	if _, err := h.exchange(t, "tenant-token"); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("Exchange error = %v; want unavailable", err)
	}
	assertCalls(t, h, "ready", "verify", "lookup", "sign")
	assertLinks(t, h, nil)
}
