package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/apps/identity/internal/config"
	"github.com/ivannguyendev/chatim/apps/identity/internal/exchange"
	"github.com/ivannguyendev/chatim/apps/identity/internal/signing"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

type wakeStore struct {
	Store
	signingKey     store.SigningKey
	tenantKey      ed25519.PrivateKey
	tenantDisabled atomic.Bool
	events         chan store.StreamEvent
}

type wakeSlot struct{}

func (wakeSlot) Renew(context.Context) (bool, error) { return true, nil }

func (wakeSlot) Release() {}

func (s *wakeStore) SigningKeys(context.Context) ([]store.SigningKey, error) {
	return []store.SigningKey{s.signingKey}, nil
}

func (s *wakeStore) TryLoadSlot(context.Context, string, int) (store.Slot, bool, error) {
	return wakeSlot{}, true, nil
}

func (s *wakeStore) StreamStart(context.Context) (store.StreamToken, error) {
	return store.StreamToken("t0"), nil
}

func (s *wakeStore) OpenStream(context.Context, store.StreamToken) (store.ChangeStream, error) {
	return wakeStream(s.events), nil
}

type wakeStream chan store.StreamEvent

func (w wakeStream) Next(ctx context.Context) (store.StreamEvent, bool, error) {
	select {
	case <-ctx.Done():
		return store.StreamEvent{}, false, ctx.Err()
	case ev := <-w:
		return ev, true, nil
	}
}

func (wakeStream) Close(context.Context) error { return nil }

func (s *wakeStore) LoadPage(_ context.Context, after store.UserKey, _ int) ([]store.DirectoryEntry, error) {
	if after.ID != "" {
		return nil, nil
	}
	return []store.DirectoryEntry{{TenantID: "shop", ID: "lan", DisplayName: "Lan", State: store.StateActive, Ver: 1}}, nil
}

func (s *wakeStore) Revocations(context.Context) (revocation.Snapshot, error) {
	return revocation.Snapshot{}, nil
}

func (s *wakeStore) TrustedIssuers(context.Context) ([]store.TrustedIssuer, error) {
	state := store.StateActive
	if s.tenantDisabled.Load() {
		state = store.StateDisabled
	}
	return []store.TrustedIssuer{{Issuer: "https://shop.example", TenantID: "shop", IssuerState: store.StateActive, TenantState: state}}, nil
}

func (s *wakeStore) TrustedKeys(context.Context) ([]store.TrustedKey, error) {
	public, _ := s.tenantKey.Public().(ed25519.PublicKey)
	jwk, err := json.Marshal(map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)})
	if err != nil {
		return nil, err
	}
	return []store.TrustedKey{{Issuer: "https://shop.example", KeyID: "shop-1", Algorithm: "EdDSA", PublicKey: jwk, State: store.StateActive}}, nil
}

func newWakeStore(t *testing.T, kek []byte) *wakeStore {
	t.Helper()
	sealer, err := signing.NewSealer(kek, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := signing.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal(k)
	if err != nil {
		t.Fatal(err)
	}
	_, tenantKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &wakeStore{
		signingKey: store.SigningKey{ID: k.ID, State: store.KeyActive, PublicKey: k.PublicKey().Key, PrivateSealed: sealed},
		tenantKey:  tenantKey, events: make(chan store.StreamEvent),
	}
}

func wakeConfig(kek []byte) config.Config {
	return config.Config{
		Issuer: "iss", Audience: "aud", TokenTTL: 5 * time.Minute, ExchangeAudience: "tenant-aud", ExchangeMaxLifetime: 10 * time.Minute,
		TrustReload: 10 * time.Second, ExchangeIPRate: 50, ExchangeIPBurst: 500, ExchangeIPTracked: 1000, KeyEncryptionKey: kek,
		DirectoryLoadSlots: 1, InflightMax: 100, UserRate: 50, UserBurst: 100, UserInflight: 32, UserLimiters: 1000,
	}
}

func (s *wakeStore) token(t *testing.T) string {
	t.Helper()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "https://shop.example", "sub": "lan", "aud": "tenant-aud", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
	})
	token.Header["kid"] = "shop-1"
	signed, err := token.SignedString(s.tenantKey)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestTenantDisableEventRefusesTheNextExchangeWithoutWaitingForATick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kek := make([]byte, signing.KEKLength)
		_, _ = rand.Read(kek)
		cfg := wakeConfig(kek)
		st := newWakeStore(t, kek)
		ring, err := newRing(cfg, st)
		if err != nil {
			t.Fatal(err)
		}
		dir, err := newDirectory(cfg, st)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := newTrust(cfg, st, dir, ring, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if err := ring.Reload(ctx); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Go(func() { _ = dir.run(ctx, func() {}) })
		wg.Go(func() { _ = tr.run(ctx, func() {}) })
		defer func() {
			cancel()
			wg.Wait()
		}()
		synctest.Wait()
		if _, err := tr.service.Exchange(ctx, st.token(t), "198.51.100.7"); err != nil {
			t.Fatalf("exchange before the disable: %v", err)
		}
		time.Sleep(time.Second)
		began := time.Now()
		st.tenantDisabled.Store(true)
		st.events <- store.StreamEvent{Kinds: []store.EventKind{store.EventTrust, store.EventRevocation}, Token: store.StreamToken("t1")}
		synctest.Wait()
		_, err = tr.service.Exchange(ctx, st.token(t), "198.51.100.7")
		if !errors.Is(err, exchange.ErrDisabled) || time.Since(began) != 0 {
			t.Fatalf("exchange after the event = %v at %v; want ErrDisabled at once, not on the %v tick", err, time.Since(began), cfg.TrustReload)
		}
	})
}
