package admincmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
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
	if err := st.CreateTenant(ctx, t, audit(env, *id, "tenant_create", "")); err != nil {
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
		if err := st.SetTenantState(ctx, *id, state, revocation(env, action), audit(env, *id, action, "")); err != nil {
			return err
		}
		_, err := fmt.Fprintf(env.Out, "tenant %s: %s\n", *id, action)
		return err
	}
}

func userFlags(name string) (flags, *string, *string) {
	f := newFlags(name)
	return f, f.set.String("tenant", "", "tenant id"), f.set.String("user", "", "user id")
}

func userState(state store.State) command {
	return func(ctx context.Context, env Env, st Store, args []string) error {
		f, tenant, user := userFlags("user-state")
		if err := f.parse(args, "tenant", "user"); err != nil {
			return err
		}
		action := map[store.State]string{store.StateActive: "user_enable", store.StateDisabled: "user_disable"}[state]
		if err := st.SetUserState(ctx, *tenant, *user, state, audit(env, *tenant, action, *user)); err != nil {
			return err
		}
		_, err := fmt.Fprintf(env.Out, "user %s: %s\n", *user, action)
		return err
	}
}
