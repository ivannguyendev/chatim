package admincmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func groupFlags(name string) (flags, *string, *string) {
	f := newFlags(name)
	return f, f.set.String("tenant", "", "tenant id"), f.set.String("id", "", "group id")
}

func groupAudit(env Env, tenant, action, id string) store.AuditEvent {
	a := audit(env, tenant, action, "", "")
	a.Detail = map[string]string{"group": id}
	return a
}

func validGroupIDs(ids ...string) error {
	for _, id := range ids {
		if err := directory.ValidateGroupID(id); err != nil {
			return err
		}
	}
	return nil
}

func groupCreate(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, id := groupFlags("group-create")
	name := f.set.String("name", "", "group name")
	parent := f.set.String("parent", "", "parent group id")
	mentionable := f.set.Bool("mentionable", true, "members can be mentioned through the group")
	if err := f.parse(args, "tenant", "id", "name"); err != nil {
		return err
	}
	if err := validGroupIDs(*id); err != nil {
		return err
	}
	if *parent != "" {
		if err := validGroupIDs(*parent); err != nil {
			return err
		}
	}
	n, err := directory.NormalizeGroupName(*name)
	if err != nil {
		return err
	}
	now := env.Now()
	g := store.Group{
		TenantID: *tenant, ID: *id, Name: n, ParentID: *parent, Mentionable: *mentionable,
		State: store.GroupLive, CreatedAt: now, UpdatedAt: now, UpdatedBy: actor,
	}
	if err := st.CreateGroup(ctx, g, groupAudit(env, *tenant, "group_create", *id)); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "group %s created\n", *id)
	return err
}

func groupUpdate(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, id := groupFlags("group-update")
	name := f.set.String("name", "", "new group name")
	parent := f.set.String("parent", "", `new parent group id; "" moves the group to the root`)
	mentionable := f.set.Bool("mentionable", true, "members can be mentioned through the group")
	if err := f.parse(args, "tenant", "id"); err != nil {
		return err
	}
	given := f.given()
	if !given["name"] && !given["parent"] && !given["mentionable"] {
		return fmt.Errorf("%w: set at least one of -name, -parent, -mentionable", errUsage)
	}
	if err := validGroupIDs(*id); err != nil {
		return err
	}
	var u store.GroupUpdate
	if given["name"] {
		n, err := directory.NormalizeGroupName(*name)
		if err != nil {
			return err
		}
		u.Name = &n
	}
	if given["parent"] {
		if *parent != "" {
			if err := validGroupIDs(*parent); err != nil {
				return err
			}
		}
		u.ParentID = parent
	}
	if given["mentionable"] {
		u.Mentionable = mentionable
	}
	if err := st.UpdateGroup(ctx, *tenant, *id, u, groupAudit(env, *tenant, "group_update", *id)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(env.Out, "group %s updated\n", *id)
	return err
}

func groupDelete(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, id := groupFlags("group-delete")
	if err := f.parse(args, "tenant", "id"); err != nil {
		return err
	}
	if err := validGroupIDs(*id); err != nil {
		return err
	}
	if err := st.DeleteGroup(ctx, *tenant, *id, groupAudit(env, *tenant, "group_delete", *id)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(env.Out, "group %s deleted\n", *id)
	return err
}

func accountGroups(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, username := accountFlags("account-groups")
	groups := f.set.String("groups", "", `comma-separated group ids; "" removes every group`)
	if err := f.parse(args, "tenant", "username"); err != nil {
		return err
	}
	if !f.given()["groups"] {
		return fmt.Errorf(`%w: -groups is required; -groups "" removes every group`, errUsage)
	}
	ids, err := splitGroups(*groups)
	if err != nil {
		return err
	}
	a, err := findAccount(ctx, st, *tenant, *username)
	if err != nil {
		return err
	}
	at := audit(env, a.TenantID, "account_groups_set", a.ID, "")
	at.Detail = map[string]string{"groups": strings.Join(ids, ",")}
	if err := st.SetAccountGroups(ctx, a.TenantID, a.ID, ids, at); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "account %s groups: %s\n", a.Username, listOrNone(ids))
	return err
}

func splitGroups(raw string) ([]string, error) {
	ids := []string{}
	if raw == "" {
		return ids, nil
	}
	seen := map[string]bool{}
	for part := range strings.SplitSeq(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			return nil, fmt.Errorf("%w: -groups has an empty group id", apperr.ErrInvalidArgument)
		}
		if err := validGroupIDs(id); err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, fmt.Errorf("%w: group %s listed twice", apperr.ErrInvalidArgument, id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

func listOrNone(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

func profileSet(ctx context.Context, env Env, st Store, args []string) error {
	f, tenant, username := accountFlags("profile-set")
	name := f.set.String("name", "", "display name")
	avatar := f.set.String("avatar", "", `https avatar url; "" clears it`)
	if err := f.parse(args, "tenant", "username"); err != nil {
		return err
	}
	given := f.given()
	if !given["name"] && !given["avatar"] {
		return fmt.Errorf("%w: set -name, -avatar or both", errUsage)
	}
	var u store.ProfileUpdate
	if given["name"] {
		n, err := directory.NormalizeDisplayName(*name)
		if err != nil {
			return err
		}
		u.DisplayName = &n
	}
	if given["avatar"] {
		if err := directory.ValidateAvatarURL(*avatar); err != nil {
			return err
		}
		u.AvatarURL = avatar
	}
	a, err := findAccount(ctx, st, *tenant, *username)
	if err != nil {
		return err
	}
	if err := st.SetProfile(ctx, a.TenantID, a.ID, u, audit(env, a.TenantID, "profile_set", a.ID, "")); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "profile updated for %s\n", a.Username)
	return err
}
