package admincmd

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

func migrate(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("migrate")
	apply := f.set.Bool("apply", false, "create missing collections and indexes")
	f.set.Bool("dry-run", true, "list what is missing or outdated only")
	if err := f.parse(args); err != nil {
		return err
	}
	return runMigrate(ctx, env, st, *apply)
}

func runMigrate(ctx context.Context, env Env, st Store, apply bool) error {
	lines, err := st.Migrate(ctx, apply)
	prefix := ""
	if apply {
		prefix = "applied: "
	}
	for _, line := range lines {
		if _, werr := fmt.Fprintln(env.Out, prefix+line); werr != nil {
			return werr
		}
	}
	if err != nil {
		return err
	}
	state, err := st.Schema(ctx)
	if err != nil {
		return err
	}
	if len(state.Missing) == 0 && len(state.Outdated) == 0 {
		_, err = fmt.Fprintln(env.Out, "schema is up to date")
	}
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
	if err := st.CreateTenant(ctx, t); err != nil {
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
		if err := st.SetTenantState(ctx, *id, state, revocation(env, action)); err != nil {
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
		if err := st.SetUserState(ctx, *tenant, *user, state, env.Now(), actor); err != nil {
			return err
		}
		_, err := fmt.Fprintf(env.Out, "user %s: %s\n", *user, action)
		return err
	}
}
