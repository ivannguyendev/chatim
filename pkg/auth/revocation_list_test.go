package auth_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/auth"
)

var now = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func newList(t *testing.T) *auth.RevocationList {
	t.Helper()
	l, err := auth.NewRevocationList(auth.RevocationListConfig{Leeway: 30 * time.Second, MaxKeep: 17 * time.Minute, MaxAhead: time.Minute})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func claims(tenant, session string, issued time.Time) auth.Claims {
	return auth.Claims{Tenant: tenant, User: "lan", Session: session, IssuedAt: issued, ExpiresAt: issued.Add(15 * time.Minute)}
}

func phone(expires time.Time) auth.RevokedSession {
	return auth.RevokedSession{ID: "iphone", Tenant: "shop", ExpiresAt: expires}
}

func TestNewRevocationListRejectsBadConfig(t *testing.T) {
	for _, cfg := range []auth.RevocationListConfig{
		{Leeway: -time.Second, MaxKeep: time.Minute, MaxAhead: time.Minute},
		{MaxKeep: 0, MaxAhead: time.Minute},
		{MaxKeep: time.Minute, MaxAhead: -time.Second},
	} {
		if _, err := auth.NewRevocationList(cfg); err == nil {
			t.Fatalf("New(%+v) accepted a bad config", cfg)
		}
	}
}

func TestMergeBlocksARevokedSessionOnly(t *testing.T) {
	l := newList(t)
	added := l.Merge(auth.RevocationSnapshot{Sessions: []auth.RevokedSession{phone(now.Add(16 * time.Minute))}}, now)
	if !slices.Equal(added.Sessions, []string{"iphone"}) || len(added.Tenants) != 0 || added.Empty() {
		t.Fatalf("added = %+v", added)
	}
	if got := l.Check(claims("shop", "iphone", now)); got != auth.SessionRevoked {
		t.Fatalf("revoked session = %v", got)
	}
	if got := l.Check(claims("shop", "laptop", now)); got != auth.Allowed {
		t.Fatalf("other session = %v", got)
	}
	if got := l.Check(claims("other", "iphone", now)); got != auth.Allowed {
		t.Fatalf("same sid in another tenant = %v", got)
	}
}

func TestMergeOnlyAddsAndReportsNewEntries(t *testing.T) {
	l := newList(t)
	snap := auth.RevocationSnapshot{Sessions: []auth.RevokedSession{phone(now.Add(16 * time.Minute))}}
	l.Merge(snap, now)
	if again := l.Merge(snap, now); !again.Empty() {
		t.Fatalf("second merge added %+v", again)
	}
	if empty := l.Merge(auth.RevocationSnapshot{}, now); !empty.Empty() {
		t.Fatalf("empty merge added %+v", empty)
	}
	if got := l.Check(claims("shop", "iphone", now)); got != auth.SessionRevoked {
		t.Fatalf("an empty snapshot removed the entry: %v", got)
	}
}

func TestTenantRevokesTokensIssuedUpToTheRevokeTime(t *testing.T) {
	l := newList(t)
	revoked := now.Add(500 * time.Millisecond)
	added := l.Merge(auth.RevocationSnapshot{Tenants: []auth.RevokedTenant{{ID: "shop", RevokedBefore: revoked, ExpiresAt: revoked.Add(16 * time.Minute)}}}, now)
	if !slices.Equal(added.Tenants, []string{"shop"}) {
		t.Fatalf("added = %+v", added)
	}
	cases := []struct {
		issued time.Time
		want   auth.Verdict
	}{
		{now.Add(-time.Minute), auth.TenantRevoked},
		{now, auth.TenantRevoked},
		{now.Add(time.Second), auth.Allowed},
	}
	for _, tc := range cases {
		if got := l.Check(claims("shop", "any", tc.issued)); got != tc.want {
			t.Fatalf("issued %v = %v; want %v", tc.issued, got, tc.want)
		}
	}
	if got := l.Check(claims("other", "any", now)); got != auth.Allowed {
		t.Fatalf("other tenant = %v", got)
	}
}

func TestTenantRevokedAgainIsReportedWithTheLaterTime(t *testing.T) {
	l := newList(t)
	first := auth.RevokedTenant{ID: "shop", RevokedBefore: now, ExpiresAt: now.Add(16 * time.Minute)}
	l.Merge(auth.RevocationSnapshot{Tenants: []auth.RevokedTenant{first}}, now)
	later := now.Add(10 * time.Minute)
	second := auth.RevokedTenant{ID: "shop", RevokedBefore: later, ExpiresAt: later.Add(16 * time.Minute)}
	added := l.Merge(auth.RevocationSnapshot{Tenants: []auth.RevokedTenant{second}}, later)
	if !slices.Equal(added.Tenants, []string{"shop"}) {
		t.Fatalf("second revoke not reported: %+v", added)
	}
	if got := l.Check(claims("shop", "any", now.Add(5*time.Minute))); got != auth.TenantRevoked {
		t.Fatalf("token issued between the two revokes = %v", got)
	}
	if old := l.Merge(auth.RevocationSnapshot{Tenants: []auth.RevokedTenant{first}}, later); !old.Empty() {
		t.Fatalf("an older revoke time was reported: %+v", old)
	}
	if got := l.Check(claims("shop", "any", now.Add(5*time.Minute))); got != auth.TenantRevoked {
		t.Fatalf("an older revoke time lowered the stored one: %v", got)
	}
}

func TestPruneKeepsEntriesForTheLeeway(t *testing.T) {
	l := newList(t)
	expires := now.Add(16 * time.Minute)
	l.Merge(auth.RevocationSnapshot{
		Sessions: []auth.RevokedSession{phone(expires)},
		Tenants:  []auth.RevokedTenant{{ID: "shop", RevokedBefore: now, ExpiresAt: expires}},
	}, now)
	l.Prune(expires.Add(29 * time.Second))
	if s, tn := l.Counts(); s != 1 || tn != 1 {
		t.Fatalf("pruned inside the leeway: %d sessions, %d tenants", s, tn)
	}
	l.Prune(expires.Add(31 * time.Second))
	if s, tn := l.Counts(); s != 0 || tn != 0 {
		t.Fatalf("kept after the leeway: %d sessions, %d tenants", s, tn)
	}
	if got := l.Check(claims("shop", "iphone", now)); got != auth.Allowed {
		t.Fatalf("after prune = %v", got)
	}
}

func TestMergeKeepsTheLaterExpiry(t *testing.T) {
	l := newList(t)
	l.Merge(auth.RevocationSnapshot{Sessions: []auth.RevokedSession{phone(now.Add(16 * time.Minute))}}, now)
	l.Merge(auth.RevocationSnapshot{Sessions: []auth.RevokedSession{phone(now.Add(5 * time.Minute))}}, now)
	l.Prune(now.Add(10 * time.Minute))
	if s, _ := l.Counts(); s != 1 {
		t.Fatal("a shorter expiry replaced the longer one")
	}
}

func TestMergeDropsEntriesOutOfBounds(t *testing.T) {
	l := newList(t)
	added := l.Merge(auth.RevocationSnapshot{
		Sessions: []auth.RevokedSession{
			{ID: "far", Tenant: "shop", ExpiresAt: now.Add(48 * time.Hour)},
			{ID: "", Tenant: "shop", ExpiresAt: now.Add(time.Minute)},
			{ID: "x", Tenant: "", ExpiresAt: now.Add(time.Minute)},
			{ID: "gone", Tenant: "shop", ExpiresAt: now.Add(-time.Hour)},
		},
		Tenants: []auth.RevokedTenant{
			{ID: "future", RevokedBefore: now.Add(time.Hour), ExpiresAt: now.Add(10 * time.Minute)},
			{ID: "long", RevokedBefore: now, ExpiresAt: now.Add(48 * time.Hour)},
			{ID: "", RevokedBefore: now, ExpiresAt: now.Add(time.Minute)},
		},
	}, now)
	if !added.Empty() {
		t.Fatalf("added out-of-bounds entries: %+v", added)
	}
	if got := l.Dropped(); got != 7 {
		t.Fatalf("dropped = %d; want 7", got)
	}
	if s, tn := l.Counts(); s != 0 || tn != 0 {
		t.Fatalf("stored %d sessions, %d tenants", s, tn)
	}
}

func TestSnapshotJSONShape(t *testing.T) {
	snap := auth.RevocationSnapshot{
		Sessions: []auth.RevokedSession{phone(now)},
		Tenants:  []auth.RevokedTenant{{ID: "shop", RevokedBefore: now, ExpiresAt: now}},
	}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"sessions":[{"session_id":"iphone","tenant_id":"shop","expires_at":"2026-10-27T09:00:00Z"}],"tenants":[{"tenant_id":"shop","revoked_before":"2026-10-27T09:00:00Z","expires_at":"2026-10-27T09:00:00Z"}]}`
	if string(body) != want {
		t.Fatalf("json = %s", body)
	}
	empty, _ := json.Marshal(auth.RevocationSnapshot{})
	if string(empty) != `{"sessions":[],"tenants":[]}` {
		t.Fatalf("empty json = %s", empty)
	}
}
