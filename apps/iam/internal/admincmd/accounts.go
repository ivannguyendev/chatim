package admincmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

func migrate(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("migrate")
	apply := f.set.Bool("apply", false, "apply pending migrations")
	f.set.Bool("dry-run", true, "list pending migrations only")
	if err := f.parse(args); err != nil {
		return err
	}
	pending, err := st.Migrate(ctx, *apply)
	if err != nil {
		return err
	}
	verb := "pending"
	if *apply {
		verb = "applied"
	}
	if len(pending) == 0 {
		_, err = fmt.Fprintln(env.Out, "schema is up to date")
		return err
	}
	_, err = fmt.Fprintf(env.Out, "%s: %s\n", verb, strings.Join(pending, ", "))
	return err
}

func tenantCreate(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("tenant-create")
	id := f.set.String("id", "", "tenant id")
	name := f.set.String("name", "", "display name")
	if err := f.parse(args, "id", "name"); err != nil {
		return err
	}
	if err := ids.ValidTenant(*id); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	now := env.Now()
	t := store.Tenant{ID: *id, Name: *name, State: store.StateActive, Ver: 1, CreatedAt: now, UpdatedAt: now, UpdatedBy: actor}
	if err := st.CreateTenant(ctx, t, audit(env, *id, "tenant_create", "", "")); err != nil {
		return err
	}
	_, err := fmt.Fprintf(env.Out, "tenant %s created\n", *id)
	return err
}

func tenantState(state store.State) command {
	return func(ctx context.Context, env Env, st Store, args []string) error {
		f := newFlags("tenant-state")
		id := f.set.String("id", "", "tenant id")
		if err := f.parse(args, "id"); err != nil {
			return err
		}
		action := map[store.State]string{store.StateActive: "tenant_enable", store.StateDisabled: "tenant_disable"}[state]
		if err := st.SetTenantState(ctx, *id, state, revocation(env, action), audit(env, *id, action, "", "")); err != nil {
			return err
		}
		_, err := fmt.Fprintf(env.Out, "tenant %s: %s\n", *id, action)
		return err
	}
}

func accountFlags(name string) (flags, *string, *string) {
	f := newFlags(name)
	return f, f.set.String("tenant", "", "tenant id"), f.set.String("username", "", "username")
}

func findAccount(ctx context.Context, st Store, tenant, username string) (store.Account, error) {
	u, err := auth.NormalizeUsername(username)
	if err != nil {
		return store.Account{}, fmt.Errorf("%w: %w", errUsage, err)
	}
	return st.AccountByUsername(ctx, tenant, u)
}

func accountCreate(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, username := accountFlags("account-create")
	email := f.set.String("email", "", "optional email")
	name := f.set.String("name", "", "display name; the username when not set")
	if err := f.parse(args, "tenant", "username"); err != nil {
		return err
	}
	u, err := auth.NormalizeUsername(*username)
	if err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	if *email != "" && !strings.Contains(*email, "@") {
		return fmt.Errorf("%w: -email must contain @", errUsage)
	}
	display := u
	if f.given()["name"] {
		if display, err = directory.NormalizeDisplayName(*name); err != nil {
			return err
		}
	}
	id, err := session.NewID(env.Random)
	if err != nil {
		return err
	}
	temp, hash, err := temporary(env)
	if err != nil {
		return err
	}
	now := env.Now()
	a := store.Account{
		TenantID: *tenant, ID: id, Username: u, DisplayName: display, Email: strings.ToLower(*email), State: store.StateActive,
		PasswordHash: hash, PasswordMustChange: true, PasswordChangedAt: now, Ver: 1, CreatedAt: now, UpdatedAt: now, UpdatedBy: actor,
	}
	if err := st.CreateAccount(ctx, a, audit(env, *tenant, "account_create", id, "")); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "account %s created for %s\ntemporary password: %s\n", id, u, temp)
	return err
}

func passwordReset(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, username := accountFlags("password-reset")
	if err := f.parse(args, "tenant", "username"); err != nil {
		return err
	}
	a, err := findAccount(ctx, st, *tenant, *username)
	if err != nil {
		return err
	}
	temp, hash, err := temporary(env)
	if err != nil {
		return err
	}
	change := store.PasswordChange{TenantID: a.TenantID, AccountID: a.ID, ExpectVer: a.Ver, Hash: hash, MustChange: true, Revocation: revocation(env, "password_reset")}
	if err := st.SetPassword(ctx, change, audit(env, a.TenantID, "password_reset", a.ID, "")); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "password reset for %s; every session revoked\ntemporary password: %s\n", a.Username, temp)
	return err
}

func accountState(state store.State) command {
	return func(ctx context.Context, env Env, st Store, args []string) error {
		f, tenant, username := accountFlags("account-state")
		if err := f.parse(args, "tenant", "username"); err != nil {
			return err
		}
		a, err := findAccount(ctx, st, *tenant, *username)
		if err != nil {
			return err
		}
		action := map[store.State]string{store.StateActive: "account_enable", store.StateDisabled: "account_disable"}[state]
		if err := st.SetAccountState(ctx, a.TenantID, a.ID, state, revocation(env, action), audit(env, a.TenantID, action, a.ID, "")); err != nil {
			return err
		}
		_, err = fmt.Fprintf(env.Out, "account %s: %s\n", a.Username, action)
		return err
	}
}

func sessionList(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, username := accountFlags("session-list")
	if err := f.parse(args, "tenant", "username"); err != nil {
		return err
	}
	a, err := findAccount(ctx, st, *tenant, *username)
	if err != nil {
		return err
	}
	sessions, err := st.ActiveSessions(ctx, a.TenantID, a.ID)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(env.Out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SESSION\tDEVICE\tCREATED\tLAST USED")
	for _, s := range sessions {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, s.DeviceName, s.CreatedAt.Format(time.RFC3339), s.LastUsedAt.Format(time.RFC3339))
	}
	return w.Flush()
}

func sessionRevoke(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("session-revoke")
	id := f.set.String("session", "", "session id")
	if err := f.parse(args, "session"); err != nil {
		return err
	}
	s, err := st.SessionByID(ctx, *id)
	if err != nil {
		return err
	}
	revoked, err := st.RevokeSession(ctx, s.ID, revocation(env, "admin"), audit(env, s.TenantID, "session_revoke", s.AccountID, s.ID))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "session %s revoked: %v\n", s.ID, revoked)
	return err
}

func temporary(env Env) (string, string, error) {
	temp, err := password.Temporary(env.Random)
	if err != nil {
		return "", "", err
	}
	hash, err := env.Hasher.Hash(temp)
	if err != nil {
		return "", "", err
	}
	return temp, hash, nil
}
