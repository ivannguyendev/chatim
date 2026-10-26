package admincmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ivannguyendev/chatim/apps/iam/internal/keyring"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const jwksFileMode = 0o644

func keyRotate(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("key-rotate")
	activate := f.set.Bool("activate-if-none", false, "activate the new key when no key is active yet")
	if err := f.parse(args); err != nil {
		return err
	}
	_, err := rotate(ctx, env, st, *activate)
	return err
}

func rotate(ctx context.Context, env Env, st Store, activateIfNone bool) (string, error) {
	k, err := signing.Generate(env.Random)
	if err != nil {
		return "", err
	}
	sealed, err := env.Sealer.Seal(k)
	if err != nil {
		return "", err
	}
	row := store.SigningKey{ID: k.ID, State: store.KeyPending, PublicKey: k.PublicKey().Key, PrivateSealed: sealed, CreatedAt: env.Now()}
	if err := st.CreateSigningKey(ctx, row, audit(env, "", "key_create", "", "")); err != nil {
		return "", err
	}
	state := "pending"
	if activateIfNone {
		active, err := hasActiveKey(ctx, st)
		if err != nil {
			return "", err
		}
		if !active {
			if err := st.ActivateSigningKey(ctx, k.ID, env.Now(), audit(env, "", "key_activate", "", "")); err != nil {
				return "", err
			}
			state = "active"
		}
	}
	_, err = fmt.Fprintf(env.Out, "signing key %s created (%s)\n", k.ID, state)
	return k.ID, err
}

func keyActivate(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("key-activate")
	kid := f.set.String("kid", "", "key id")
	if err := f.parse(args, "kid"); err != nil {
		return err
	}
	if err := st.ActivateSigningKey(ctx, *kid, env.Now(), audit(env, "", "key_activate", "", "")); err != nil {
		return err
	}
	_, err := fmt.Fprintf(env.Out, "signing key %s active\n", *kid)
	return err
}

func jwksExport(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("jwks-export")
	out := f.set.String("out", "", "file to write")
	merge := f.set.String("merge", "", "existing JWKS file whose public keys are kept")
	if err := f.parse(args, "out"); err != nil {
		return err
	}
	return export(ctx, env, st, *out, *merge)
}

func export(ctx context.Context, env Env, st Store, out, merge string) error {
	keys, err := st.SigningKeys(ctx)
	if err != nil {
		return err
	}
	var existing []byte
	if merge != "" {
		existing, err = os.ReadFile(merge)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	doc, err := signing.Merge(existing, keyring.Published(keys, env.Now(), env.KeyWindow))
	if err != nil {
		return err
	}
	if err := writeAtomic(out, doc); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "wrote %s\n", out)
	return err
}

func bootstrap(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("bootstrap")
	jwks := f.set.String("jwks", "", "JWKS file to create or merge into")
	if err := f.parse(args, "jwks"); err != nil {
		return err
	}
	if _, err := st.Migrate(ctx, true); err != nil {
		return err
	}
	active, err := hasActiveKey(ctx, st)
	if err != nil {
		return err
	}
	if !active {
		if _, err := rotate(ctx, env, st, true); err != nil {
			return err
		}
	}
	return export(ctx, env, st, *jwks, *jwks)
}

func hasActiveKey(ctx context.Context, st Store) (bool, error) {
	keys, err := st.SigningKeys(ctx)
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		if k.State == store.KeyActive {
			return true, nil
		}
	}
	return false, nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".jwks-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, jwksFileMode)); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
