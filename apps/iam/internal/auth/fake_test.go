package auth_test

import (
	"context"
	"crypto/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/guard"
	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var t0 = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

type fakeStore struct {
	mu       sync.Mutex
	tenants  map[string]store.Tenant
	accounts map[string]store.Account
	sessions map[string]store.Session
	audits   []string
	failRead error
	failRev  error
	issued   time.Time
	profiles map[string]store.Profile
	profile  profileCalls
}

func newFakeStore() *fakeStore {
	return &fakeStore{tenants: map[string]store.Tenant{}, accounts: map[string]store.Account{}, sessions: map[string]store.Session{}, profiles: map[string]store.Profile{}}
}

func accountKey(tenant, id string) string { return tenant + "|" + id }

func (f *fakeStore) TenantByID(_ context.Context, id string) (store.Tenant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tenants[id]
	if !ok {
		return store.Tenant{}, apperr.ErrNotFound
	}
	return t, nil
}

func (f *fakeStore) AccountByUsername(_ context.Context, tenant, username string) (store.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.accounts {
		if a.TenantID == tenant && a.Username == username {
			return a, nil
		}
	}
	return store.Account{}, apperr.ErrNotFound
}

func (f *fakeStore) AccountByID(_ context.Context, tenant, id string) (store.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRead != nil {
		return store.Account{}, f.failRead
	}
	a, ok := f.accounts[accountKey(tenant, id)]
	if !ok {
		return store.Account{}, apperr.ErrNotFound
	}
	return a, nil
}

func (f *fakeStore) CreateSession(_ context.Context, n store.NewSession, audit store.AuditEvent) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := accountKey(n.Session.TenantID, n.Session.AccountID)
	a := f.accounts[key]
	if a.Ver != n.AccountVer || a.State != store.StateActive || f.tenants[a.TenantID].State != store.StateActive {
		return time.Time{}, apperr.ErrUnavailable
	}
	a.FailedLoginCount, a.NextLoginAt = 0, time.Time{}
	if n.Rehash != "" {
		a.PasswordHash, a.Ver = n.Rehash, a.Ver+1
	}
	f.accounts[key] = a
	f.sessions[n.Session.ID] = n.Session
	f.audits = append(f.audits, audit.Action)
	return f.issued, nil
}

func (f *fakeStore) RecordLoginFailure(_ context.Context, tenant, id string, next func(int) time.Time, audit store.AuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.accounts[accountKey(tenant, id)]
	a.FailedLoginCount++
	a.NextLoginAt = next(a.FailedLoginCount)
	f.accounts[accountKey(tenant, id)] = a
	f.audits = append(f.audits, audit.Action)
	return nil
}

func (f *fakeStore) SessionByID(_ context.Context, id string) (store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return store.Session{}, apperr.ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) RotateRefresh(_ context.Context, r store.Rotation, audit store.AuditEvent) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[r.SessionID]
	if s.State != store.StateActive || s.RefreshVer != r.ExpectVer {
		return time.Time{}, apperr.ErrUnavailable
	}
	s.RefreshVer++
	s.RefreshHash, s.History, s.RecentJTI, s.IdleExpiresAt, s.LastUsedAt = r.RefreshHash, r.History, r.RecentJTI, r.IdleExpiresAt, r.LastUsedAt
	f.sessions[r.SessionID] = s
	f.audits = append(f.audits, audit.Action)
	return f.issued, nil
}

func (f *fakeStore) RevokeSession(_ context.Context, id string, r store.Revocation, audit store.AuditEvent) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRev != nil {
		return false, f.failRev
	}
	s, ok := f.sessions[id]
	if !ok || s.State != store.StateActive {
		return false, nil
	}
	s.State, s.RevokedReason = store.StateDisabled, r.Reason
	f.sessions[id] = s
	f.audits = append(f.audits, audit.Action)
	return true, nil
}

func (f *fakeStore) ActiveSessions(_ context.Context, tenant, account string) ([]store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Session
	for _, s := range f.sessions {
		if s.TenantID == tenant && s.AccountID == account && s.State == store.StateActive {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeStore) SetPassword(_ context.Context, c store.PasswordChange, audit store.AuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := accountKey(c.TenantID, c.AccountID)
	a := f.accounts[key]
	if a.Ver != c.ExpectVer {
		return apperr.ErrUnavailable
	}
	a.PasswordHash, a.PasswordMustChange, a.Ver = c.Hash, c.MustChange, a.Ver+1
	f.accounts[key] = a
	for id, s := range f.sessions {
		if s.TenantID == c.TenantID && s.AccountID == c.AccountID && s.State == store.StateActive {
			s.State, s.RevokedReason = store.StateDisabled, c.Revocation.Reason
			f.sessions[id] = s
		}
	}
	f.audits = append(f.audits, audit.Action)
	return nil
}

type countingHasher struct {
	inner  *password.Hasher
	mu     sync.Mutex
	hashes int
}

func (h *countingHasher) count() {
	h.mu.Lock()
	h.hashes++
	h.mu.Unlock()
}

func (h *countingHasher) Hash(pw string) (string, error) {
	h.count()
	return h.inner.Hash(pw)
}

func (h *countingHasher) Verify(encoded, pw string) (bool, error) {
	h.count()
	return h.inner.Verify(encoded, pw)
}

func (h *countingHasher) VerifyDummy(pw string) {
	h.count()
	h.inner.VerifyDummy(pw)
}

func (h *countingHasher) NeedsRehash(encoded string) bool { return h.inner.NeedsRehash(encoded) }

func (h *countingHasher) Hashes() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hashes
}

type staticSigners struct{ signer *signing.Signer }

func (s staticSigners) Current() (*signing.Signer, error) { return s.signer, nil }

type harness struct {
	svc    *auth.Service
	store  *fakeStore
	hasher *countingHasher
	slots  *guard.HashSlots
	now    time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWithSlots(t, 4)
}

func newHarnessWithSlots(t *testing.T, slots int) *harness {
	t.Helper()
	inner, err := password.NewHasher(password.Params{Memory: 64, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := signing.Generate(rand.Reader)
	signer, _ := signing.NewSigner(key, signing.TokenConfig{Issuer: "iss", Audience: "aud", TTL: 15 * time.Minute})
	h := &harness{store: newFakeStore(), hasher: &countingHasher{inner: inner}, slots: guard.NewHashSlots(slots), now: t0}
	h.svc = auth.New(auth.Deps{
		Store: h.store, Hasher: h.hasher, Policy: password.Policy{MinLength: 12},
		Backoff: guard.Backoff{FreeFailures: 5, MaxDelay: time.Minute},
		IPs:     guard.NewIPLimiter(1000, 1000, 100), Slots: h.slots,
		Signers: staticSigners{signer: signer}, Random: rand.Reader, Now: func() time.Time { return h.now },
	}, auth.Config{SessionIdle: 30 * 24 * time.Hour, SessionMax: 90 * 24 * time.Hour, RefreshGrace: 30 * time.Second})
	h.store.tenants["shop"] = store.Tenant{ID: "shop", State: store.StateActive, Ver: 1}
	h.store.issued = t0
	return h
}

func (h *harness) addAccount(t *testing.T, id, username, pw string, mustChange bool) {
	t.Helper()
	hash, err := h.hasher.inner.Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	h.store.accounts[accountKey("shop", id)] = store.Account{TenantID: "shop", ID: id, Username: username, State: store.StateActive, PasswordHash: hash, PasswordMustChange: mustChange, Ver: 1}
}

func (h *harness) login(username, pw, jkt string) (auth.Tokens, error) {
	return h.svc.Login(context.Background(), auth.LoginRequest{
		Tenant: "shop", Username: username, Password: pw, DeviceName: "iPhone", IP: "10.0.0.1",
		Proof: auth.Proof{Thumbprint: jkt, JTI: "login-" + jkt},
	})
}

func (h *harness) refresh(token, jkt, jti string) (auth.Tokens, error) {
	return h.svc.Refresh(context.Background(), auth.RefreshRequest{Tenant: "shop", RefreshToken: token, Proof: auth.Proof{Thumbprint: jkt, JTI: jti}})
}

func sidOf(token string) string {
	sid, _, _ := strings.Cut(token, ".")
	return sid
}
