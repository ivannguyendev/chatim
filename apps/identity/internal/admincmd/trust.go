package admincmd

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

const trustNameMaxBytes = 255

var (
	trustedAlgorithms = []string{trust.AlgorithmEdDSA, trust.AlgorithmES256, trust.AlgorithmRS256}
	errOwnIssuer      = errors.New("issuer is this node's own JWT_ISSUER")
	errKeyIDMismatch  = errors.New("jwk kid differs from -kid")
)

func issuerAdd(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("issuer-add")
	issuer := f.set.String("issuer", "", "iss of the tenant tokens")
	tenant := f.set.String("tenant", "", "tenant id")
	realm := f.set.String("realm", "", "realm the issuer's subjects belong to; defaults to the issuer")
	if err := f.parse(args, "issuer", "tenant"); err != nil {
		return err
	}
	if err := checkTrustName("issuer", *issuer); err != nil {
		return err
	}
	if *realm != "" {
		if err := checkTrustName("realm", *realm); err != nil {
			return err
		}
	}
	if err := ids.ValidTenant(*tenant); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	if *issuer == env.Issuer {
		return errOwnIssuer
	}
	if err := st.CreateTrustedIssuer(ctx, *issuer, *tenant, *realm, env.Now(), actor); err != nil {
		return err
	}
	_, err := fmt.Fprintf(env.Out, "issuer %s trusted for tenant %s, realm %s\n", *issuer, *tenant, cmp.Or(*realm, *issuer))
	return err
}

func issuerState(state store.State) command {
	return func(ctx context.Context, env Env, st Store, args []string) error {
		f := newFlags("issuer-state")
		issuer := f.set.String("issuer", "", "iss of the tenant tokens")
		if err := f.parse(args, "issuer"); err != nil {
			return err
		}
		if err := checkTrustName("issuer", *issuer); err != nil {
			return err
		}
		if err := knownIssuer(ctx, st, *issuer); err != nil {
			return err
		}
		action := map[store.State]string{store.StateActive: "issuer_enable", store.StateDisabled: "issuer_disable"}[state]
		if err := st.SetTrustedIssuerState(ctx, *issuer, state, env.Now(), actor); err != nil {
			return err
		}
		_, err := fmt.Fprintf(env.Out, "issuer %s: %s\n", *issuer, action)
		return err
	}
}

func issuerKeyAdd(ctx context.Context, env Env, st Store, args []string) error {
	f, issuer, kid := issuerKeyFlags("issuer-key-add")
	alg := f.set.String("alg", "", "EdDSA, ES256 or RS256")
	jwk := f.set.String("jwk", "", "public JWK as JSON")
	if err := f.parse(args, "issuer", "kid", "alg", "jwk"); err != nil {
		return err
	}
	if err := checkIssuerKey(*issuer, *kid); err != nil {
		return err
	}
	if !slices.Contains(trustedAlgorithms, *alg) {
		return fmt.Errorf("%w: -alg must be one of %v", errUsage, trustedAlgorithms)
	}
	_, canonical, err := trust.ParsePublicJWK(*alg, []byte(*jwk))
	if err != nil {
		return err
	}
	if err := sameKeyID(canonical, *kid); err != nil {
		return err
	}
	if err := knownIssuer(ctx, st, *issuer); err != nil {
		return err
	}
	k := store.TrustedKey{Issuer: *issuer, KeyID: *kid, Algorithm: *alg, PublicKey: canonical}
	if err := st.AddTrustedKey(ctx, k, env.Now(), actor); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "issuer %s key %s added (%s)\n", *issuer, *kid, *alg)
	return err
}

func issuerKeyRemove(ctx context.Context, env Env, st Store, args []string) error {
	f, issuer, kid := issuerKeyFlags("issuer-key-remove")
	if err := f.parse(args, "issuer", "kid"); err != nil {
		return err
	}
	if err := checkIssuerKey(*issuer, *kid); err != nil {
		return err
	}
	if err := knownIssuer(ctx, st, *issuer); err != nil {
		return err
	}
	if err := st.RemoveTrustedKey(ctx, *issuer, *kid); err != nil {
		return err
	}
	_, err := fmt.Fprintf(env.Out, "issuer %s key %s removed\n", *issuer, *kid)
	return err
}

func issuerKeys(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("issuer-keys")
	issuer := f.set.String("issuer", "", "iss of the tenant tokens")
	if err := f.parse(args, "issuer"); err != nil {
		return err
	}
	if err := checkTrustName("issuer", *issuer); err != nil {
		return err
	}
	if err := knownIssuer(ctx, st, *issuer); err != nil {
		return err
	}
	keys, err := st.TrustedKeysOf(ctx, *issuer)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(env.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "KID\tALG\tSTATE\tCREATED_AT")
	for _, k := range keys {
		state := "removed"
		if k.State == store.StateActive {
			state = "active"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k.KeyID, k.Algorithm, state, k.CreatedAt.UTC().Format(time.RFC3339))
	}
	return w.Flush()
}

func issuerKeyFlags(name string) (flags, *string, *string) {
	f := newFlags(name)
	return f, f.set.String("issuer", "", "iss of the tenant tokens"), f.set.String("kid", "", "key id")
}

func checkIssuerKey(issuer, kid string) error {
	return errors.Join(checkTrustName("issuer", issuer), checkTrustName("kid", kid))
}

func checkTrustName(name, value string) error {
	if len(value) == 0 || len(value) > trustNameMaxBytes {
		return fmt.Errorf("%w: -%s must be 1 to %d bytes", errUsage, name, trustNameMaxBytes)
	}
	for i := range len(value) {
		if value[i] <= ' ' || value[i] > '~' {
			return fmt.Errorf("%w: -%s must be printable ASCII without spaces", errUsage, name)
		}
	}
	return nil
}

func sameKeyID(canonical []byte, kid string) error {
	var key struct {
		KeyID string `json:"kid"`
	}
	if err := json.Unmarshal(canonical, &key); err != nil {
		return err
	}
	if key.KeyID != "" && key.KeyID != kid {
		return fmt.Errorf("%w: %q", errKeyIDMismatch, key.KeyID)
	}
	return nil
}

func knownIssuer(ctx context.Context, st Store, issuer string) error {
	issuers, err := st.TrustedIssuers(ctx)
	if err != nil {
		return err
	}
	for _, i := range issuers {
		if i.Issuer == issuer {
			return nil
		}
	}
	return fmt.Errorf("issuer %s: %w", issuer, apperr.ErrNotFound)
}
