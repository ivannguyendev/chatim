package trust

import (
	"cmp"
	"context"
	"crypto"
	"errors"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

type snapshot struct {
	issuers map[string]issuerEntry
}

type issuerEntry struct {
	tenant       string
	realm        string
	tenantActive bool
	keys         map[string]trustedKey
}

type trustedKey struct {
	algorithm string
	public    crypto.PublicKey
}

type KeyRef struct {
	Issuer string
	KeyID  string
}

type LoadResult struct {
	Issuers int
	Keys    int
	Skipped []KeyRef
}

func (v *Verifier) Load(ctx context.Context, src store.TrustSource) (LoadResult, error) {
	issuers, err := src.TrustedIssuers(ctx)
	if err == nil {
		var keys []store.TrustedKey
		if keys, err = src.TrustedKeys(ctx); err == nil {
			snap, res := buildSnapshot(issuers, keys)
			v.stats.reloadErrors.Add(uint64(len(res.Skipped)))
			v.snap.Store(snap)
			return res, nil
		}
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		v.stats.reloadErrors.Add(1)
	}
	return LoadResult{}, fmt.Errorf("load trusted issuers: %w", err)
}

func (v *Verifier) Ready() bool { return v.snap.Load() != nil }

func buildSnapshot(issuers []store.TrustedIssuer, keys []store.TrustedKey) (*snapshot, LoadResult) {
	snap := &snapshot{issuers: make(map[string]issuerEntry, len(issuers))}
	for _, iss := range issuers {
		if iss.IssuerState != store.StateActive {
			continue
		}
		snap.issuers[iss.Issuer] = issuerEntry{
			tenant:       iss.TenantID,
			realm:        cmp.Or(iss.Realm, iss.Issuer),
			tenantActive: iss.TenantState == store.StateActive,
			keys:         make(map[string]trustedKey),
		}
	}
	res := LoadResult{Issuers: len(snap.issuers)}
	for _, k := range keys {
		entry, ok := snap.issuers[k.Issuer]
		if !ok || k.State != store.StateActive {
			continue
		}
		public, _, err := ParsePublicJWK(k.Algorithm, k.PublicKey)
		if err != nil {
			res.Skipped = append(res.Skipped, KeyRef{Issuer: k.Issuer, KeyID: k.KeyID})
			continue
		}
		entry.keys[k.KeyID] = trustedKey{algorithm: k.Algorithm, public: public}
		res.Keys++
	}
	return snap, res
}
