package exchange_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/exchange"
	"github.com/ivannguyendev/chatim/apps/identity/internal/signing"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	acmeIssuer = "https://acme.example"
	acmeKeyID  = "acme-1"
	chatIssuer = "https://chat.example"
	chatAud    = "chat"
	tokenTTL   = 5 * time.Minute
	clientIP   = "203.0.113.9"
	knownID    = "k7q2m9xw4rbd6tjhn3vcpa5e8f"
)

var (
	testNow  = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	errStore = errors.New("store down")
)

type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

type fakeVerifier struct {
	rec     *recorder
	subject trust.Subject
	err     error
}

func (f *fakeVerifier) Verify(string) (trust.Subject, error) {
	f.rec.add("verify")
	return f.subject, f.err
}

type fakeDirectory struct {
	rec      *recorder
	notReady bool
	users    map[string]directory.User
}

func (f *fakeDirectory) Ready() bool {
	f.rec.add("ready")
	return !f.notReady
}

func (f *fakeDirectory) LookupSubject(tenant, realm, sub string) (directory.User, bool) {
	f.rec.add("lookup")
	u, ok := f.users[subjectKey(tenant, realm, sub)]
	return u, ok
}

func subjectKey(tenant, realm, sub string) string { return tenant + "/" + realm + "/" + sub }

type fakeProfiles struct {
	rec     *recorder
	writes  []store.ProfileWrite
	written bool
	err     error
	links   []store.NewUser
	link    func(store.NewUser) (store.LinkedUser, error)
}

func (f *fakeProfiles) WriteProfile(_ context.Context, w store.ProfileWrite) (bool, error) {
	f.rec.add("write")
	f.writes = append(f.writes, w)
	return f.written, f.err
}

func (f *fakeProfiles) LinkUser(_ context.Context, u store.NewUser) (store.LinkedUser, error) {
	f.rec.add("link")
	f.links = append(f.links, u)
	if f.link != nil {
		return f.link(u)
	}
	return store.LinkedUser{ID: u.CandidateID, Result: store.LinkCreated}, nil
}

type fakeSigners struct {
	rec    *recorder
	signer *signing.Signer
}

func (f *fakeSigners) Current() (*signing.Signer, error) {
	f.rec.add("sign")
	if f.signer == nil {
		return nil, errors.New("no active signing key")
	}
	return f.signer, nil
}

type trustSource struct {
	tenantState store.State
	public      ed25519.PublicKey
}

func (s trustSource) TrustedIssuers(context.Context) ([]store.TrustedIssuer, error) {
	return []store.TrustedIssuer{{Issuer: acmeIssuer, TenantID: "acme", IssuerState: store.StateActive, TenantState: s.tenantState}}, nil
}

func (s trustSource) TrustedKeys(context.Context) ([]store.TrustedKey, error) {
	return edKeys(s.public, acmeIssuer)
}

func edKeys(public ed25519.PublicKey, issuers ...string) ([]store.TrustedKey, error) {
	jwk, err := json.Marshal(map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)})
	if err != nil {
		return nil, err
	}
	keys := make([]store.TrustedKey, len(issuers))
	for i, iss := range issuers {
		keys[i] = store.TrustedKey{Issuer: iss, KeyID: acmeKeyID, Algorithm: "EdDSA", PublicKey: jwk, State: store.StateActive}
	}
	return keys, nil
}

func loadedVerifier(t *testing.T, src store.TrustSource) *trust.Verifier {
	t.Helper()
	v, err := trust.New(trust.Config{Audience: "chatim", MaxLifetime: 10 * time.Minute, Leeway: 30 * time.Second, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Load(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	return v
}

func tenantToken(t *testing.T, private ed25519.PrivateKey, iss, sub, audience string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": iss, "sub": sub, "aud": audience, "iat": testNow.Unix(), "exp": testNow.Add(time.Minute).Unix(),
	})
	token.Header["kid"] = acmeKeyID
	signed, err := token.SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func trustRefusal(t *testing.T, tenantState store.State, audience string, want trust.Reason) error {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v := loadedVerifier(t, trustSource{tenantState: tenantState, public: public})
	_, err = v.Verify(tenantToken(t, private, acmeIssuer, "ha", audience))
	if !errors.Is(err, trust.ErrRefused) || trust.ReasonOf(err) != want {
		t.Fatalf("trust refusal = %v (reason %q); want reason %q", err, trust.ReasonOf(err), want)
	}
	return err
}

type harness struct {
	rec      *recorder
	verifier *fakeVerifier
	dir      *fakeDirectory
	profiles *fakeProfiles
	signers  *fakeSigners
	public   ed25519.PublicKey
	logs     *bytes.Buffer
	stats    *exchange.Stats
	svc      *exchange.Service
}

func newHarness(t *testing.T, mods ...func(*exchange.Deps)) *harness {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.NewSigner(signing.Key{ID: "k-test", Private: private},
		signing.TokenConfig{Issuer: chatIssuer, Audience: chatAud, TTL: tokenTTL})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	h := &harness{
		rec:      rec,
		verifier: &fakeVerifier{rec: rec, subject: subject("ha")},
		dir:      &fakeDirectory{rec: rec, users: map[string]directory.User{}},
		profiles: &fakeProfiles{rec: rec, written: true},
		signers:  &fakeSigners{rec: rec, signer: signer},
		public:   public,
		logs:     &bytes.Buffer{},
		stats:    &exchange.Stats{},
	}
	deps := exchange.Deps{
		Verifier:  h.verifier,
		Directory: h.dir,
		Profiles:  h.profiles,
		Signers:   h.signers,
		Random:    rand.Reader,
		Now:       func() time.Time { return testNow },
		Stats:     h.stats,
		Log:       slog.New(slog.NewTextHandler(h.logs, nil)),
	}
	for _, mod := range mods {
		mod(&deps)
	}
	h.svc = exchange.New(deps)
	return h
}

func subject(user string) trust.Subject {
	return trust.Subject{Tenant: "acme", User: user, Issuer: acmeIssuer, Realm: acmeIssuer, KeyID: acmeKeyID}
}

func (h *harness) exchange(t *testing.T, token string) (exchange.Issued, error) {
	t.Helper()
	return h.svc.Exchange(t.Context(), token, clientIP)
}

func (h *harness) cache(name, avatar string, state store.State) {
	h.dir.users[subjectKey("acme", acmeIssuer, "ha")] = directory.User{ID: knownID,
		Entry: directory.Entry{DisplayName: name, AvatarURL: avatar, State: state, Ver: 4}}
}

type issuedClaims struct {
	jwt.RegisteredClaims
	Tenant  string `json:"tid"`
	Session string `json:"sid"`
}

func (h *harness) parse(t *testing.T, token string) issuedClaims {
	t.Helper()
	var c issuedClaims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return h.public, nil },
		jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithTimeFunc(func() time.Time { return testNow }))
	if err != nil {
		t.Fatalf("issued token does not verify: %v", err)
	}
	return c
}

func unsignedToken(t *testing.T, header, claims map[string]any) string {
	t.Helper()
	part := func(v map[string]any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return part(header) + "." + part(claims) + ".c2lnbmF0dXJl"
}
