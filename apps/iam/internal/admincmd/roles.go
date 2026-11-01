package admincmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func roleFlags(name string) (flags, *string, *string) {
	f := newFlags(name)
	return f, f.set.String("tenant", "", "tenant id"), f.set.String("id", "", "role id")
}

func roleAudit(env Env, tenant, action, id string) store.AuditEvent {
	a := audit(env, tenant, action, "", "")
	a.Detail = map[string]string{"role": id}
	return a
}

func roleCreate(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, id := roleFlags("role-create")
	name := f.set.String("name", "", "role name")
	permissions := f.set.String("permissions", "", "comma-separated permissions")
	if err := f.parse(args, "tenant", "id", "name", "permissions"); err != nil {
		return err
	}
	if err := authz.ValidateRoleID(*id); err != nil {
		return err
	}
	n, err := directory.NormalizeRoleName(*name)
	if err != nil {
		return err
	}
	perms, err := splitPermissions(*permissions)
	if err != nil {
		return err
	}
	now := env.Now()
	r := store.Role{
		TenantID: *tenant, ID: *id, Name: n, Ver: 1, Permissions: perms,
		CreatedAt: now, UpdatedAt: now, UpdatedBy: actor,
	}
	if err := st.CreateRole(ctx, r, roleAudit(env, *tenant, "role_create", *id)); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "role %s created\n", *id)
	return err
}

func roleUpdate(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, id := roleFlags("role-update")
	name := f.set.String("name", "", "new role name")
	permissions := f.set.String("permissions", "", `comma-separated permissions replacing the current ones; "" removes every permission`)
	if err := f.parse(args, "tenant", "id"); err != nil {
		return err
	}
	given := f.given()
	if !given["name"] && !given["permissions"] {
		return fmt.Errorf("%w: set -name, -permissions or both", errUsage)
	}
	if err := authz.ValidateRoleID(*id); err != nil {
		return err
	}
	var u store.RoleUpdate
	if given["name"] {
		n, err := directory.NormalizeRoleName(*name)
		if err != nil {
			return err
		}
		u.Name = &n
	}
	if given["permissions"] {
		perms, err := splitPermissions(*permissions)
		if err != nil {
			return err
		}
		u.Permissions = &perms
	}
	if err := st.UpdateRole(ctx, *tenant, *id, u, roleAudit(env, *tenant, "role_update", *id)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(env.Out, "role %s updated\n", *id)
	return err
}

func roleDelete(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, id := roleFlags("role-delete")
	if err := f.parse(args, "tenant", "id"); err != nil {
		return err
	}
	if err := authz.ValidateRoleID(*id); err != nil {
		return err
	}
	removed, err := st.DeleteRole(ctx, *tenant, *id, roleAudit(env, *tenant, "role_delete", *id))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "role %s deleted; unassigned from %d accounts and %d groups\n", *id, removed.Accounts, removed.Groups)
	return err
}

func roleList(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("role-list")
	tenant := f.set.String("tenant", "", "tenant id")
	if err := f.parse(args, "tenant"); err != nil {
		return err
	}
	roles, err := st.ListRoles(ctx, *tenant)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(env.Out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ROLE\tNAME\tBUILTIN\tPERMISSIONS")
	for _, r := range roles {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.ID, r.Name, yesNo(r.Builtin), rolePermissions(r))
	}
	return w.Flush()
}

func rolePermissions(r store.Role) string {
	if r.Builtin && r.ID == authz.TenantAdminRole {
		return "all"
	}
	return listOrNone(r.Permissions)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func accountRoles(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, username := accountFlags("account-roles")
	roles := f.set.String("roles", "", `comma-separated role ids; "" removes every role`)
	if err := f.parse(args, "tenant", "username"); err != nil {
		return err
	}
	if !f.given()["roles"] {
		return fmt.Errorf(`%w: -roles is required; -roles "" removes every role`, errUsage)
	}
	ids, err := splitRoles(*roles)
	if err != nil {
		return err
	}
	a, err := findAccount(ctx, st, *tenant, *username)
	if err != nil {
		return err
	}
	at := audit(env, a.TenantID, "account_roles_set", a.ID, "")
	at.Detail = map[string]string{"roles": strings.Join(ids, ",")}
	if err := st.SetAccountRoles(ctx, a.TenantID, a.ID, ids, at); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "account %s roles: %s\n", a.Username, listOrNone(ids))
	return err
}

func groupRoles(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("group-roles")
	tenant := f.set.String("tenant", "", "tenant id")
	group := f.set.String("group", "", "group id")
	roles := f.set.String("roles", "", `comma-separated role ids; "" removes every role`)
	if err := f.parse(args, "tenant", "group"); err != nil {
		return err
	}
	if !f.given()["roles"] {
		return fmt.Errorf(`%w: -roles is required; -roles "" removes every role`, errUsage)
	}
	if err := validGroupIDs(*group); err != nil {
		return err
	}
	ids, err := splitRoles(*roles)
	if err != nil {
		return err
	}
	at := groupAudit(env, *tenant, "group_roles_set", *group)
	at.Detail["roles"] = strings.Join(ids, ",")
	if err := st.SetGroupRoles(ctx, *tenant, *group, ids, at); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "group %s roles: %s\n", *group, listOrNone(ids))
	return err
}

func splitRoles(raw string) ([]string, error) {
	return splitList(raw, "roles", "role", authz.ValidateRoleID)
}

func splitPermissions(raw string) ([]string, error) {
	list, err := splitList(raw, "permissions", "permission", func(string) error { return nil })
	if err != nil {
		return nil, err
	}
	perms, err := authz.ParseAll(list)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(perms))
	for i, p := range perms {
		out[i] = string(p)
	}
	return out, nil
}

func splitList(raw, flag, noun string, validate func(string) error) ([]string, error) {
	ids := []string{}
	if raw == "" {
		return ids, nil
	}
	seen := map[string]bool{}
	for part := range strings.SplitSeq(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			return nil, fmt.Errorf("%w: -%s has an empty %s id", apperr.ErrInvalidArgument, flag, noun)
		}
		if err := validate(id); err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, fmt.Errorf("%w: %s %s listed twice", apperr.ErrInvalidArgument, noun, id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}
