package manage_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const shop = "shop"

var itAdminPermissions = []string{
	"iam:accounts.read", "iam:groups.members.write", "iam:groups.read", "iam:groups.write", "iam:roles.assign",
}

type cast struct {
	pgWorld
	lan, binh, minh, ha auth.Caller
}

func openStory(t *testing.T) cast {
	t.Helper()
	w := openWorld(t, 8, 4)
	w.tenant(t, shop)
	c := cast{pgWorld: w}
	c.lan, c.binh, c.minh, c.ha = w.account(t, shop, "lan"), w.account(t, shop, "binh"), w.account(t, shop, "minh"), w.account(t, shop, "ha")
	for _, g := range [][2]string{{"cong-ty", ""}, {"it", "cong-ty"}, {"it-hcm", "it"}, {"admins", ""}, {"hr", ""}, {"ops", ""}} {
		w.group(t, shop, g[0], g[1])
	}
	w.role(t, shop, "it-admin", itAdminPermissions...)
	w.role(t, shop, "hr-admin", "iam:roles.read", "iam:roles.write")
	w.role(t, shop, "role-reader", "iam:roles.read")
	w.groupRoles(t, shop, "it", "it-admin")
	w.groupRoles(t, shop, "hr", "hr-admin")
	w.groupRoles(t, shop, "admins", "tenant-admin")
	w.accountRoles(t, shop, "lan", "tenant-admin")
	w.accountRoles(t, shop, "minh", "it-admin", "role-reader")
	w.joinGroups(t, shop, "binh", "it-hcm")
	return c
}

func (c cast) listGroups(caller auth.Caller) error {
	_, _, err := c.svc.ListGroups(context.Background(), caller, "")
	return err
}

func (c cast) listRoles(caller auth.Caller) error {
	_, _, err := c.svc.ListRoles(context.Background(), caller, "")
	return err
}

func (c cast) move(caller auth.Caller, id, parent string) error {
	_, err := c.svc.UpdateGroup(context.Background(), caller, id, store.GroupUpdate{ParentID: &parent})
	return err
}

func TestStoryGrandchildInheritsAndMoveFollows(t *testing.T) {
	ctx := context.Background()
	c := openStory(t)
	must(t, "binh lists groups through it", c.listGroups(c.binh))
	wantCode(t, "binh lists roles before the grant", c.listRoles(c.binh), apperr.ErrPermissionDenied)
	must(t, "lan binds role-reader to cong-ty", c.svc.AssignGroupRole(ctx, c.lan, "cong-ty", "role-reader"))
	must(t, "binh in the grandchild it-hcm lists roles", c.listRoles(c.binh))
	must(t, "lan moves it-hcm under ops", c.move(c.lan, "it-hcm", "ops"))
	wantCode(t, "binh lists roles under ops", c.listRoles(c.binh), apperr.ErrPermissionDenied)
	wantCode(t, "binh lists groups under ops", c.listGroups(c.binh), apperr.ErrPermissionDenied)
	must(t, "lan moves it-hcm under hr", c.move(c.lan, "it-hcm", "hr"))
	must(t, "binh lists roles under hr", c.listRoles(c.binh))
	wantCode(t, "binh lists groups under hr", c.listGroups(c.binh), apperr.ErrPermissionDenied)
	wantList(t, "parent of it-hcm", c.parentOf(t, shop, "it-hcm"), []string{"hr"})
}

func TestStoryBinhCannotJoinAdmins(t *testing.T) {
	ctx := context.Background()
	c := openStory(t)
	err := c.svc.SetAccountGroups(ctx, c.binh, "binh", []string{"admins", "it-hcm"})
	wantCode(t, "binh joins admins", err, apperr.ErrPermissionDenied)
	wantList(t, "groups of binh", c.groupsOf(t, shop, "binh"), []string{"it-hcm"})
	if n := c.audits(t, shop, "binh", "account_groups_set"); n != 0 {
		t.Fatalf("audits of the denied call = %d; want 0", n)
	}
	must(t, "binh puts ha in it-hcm", c.svc.SetAccountGroups(ctx, c.binh, "ha", []string{"it-hcm"}))
	wantList(t, "groups of ha", c.groupsOf(t, shop, "ha"), []string{"it-hcm"})
	if n := c.audits(t, shop, "binh", "account_groups_set"); n != 1 {
		t.Fatalf("audits after the allowed call = %d; want 1", n)
	}
}

func TestStoryMoveToAStrongerBranchIsDenied(t *testing.T) {
	c := openStory(t)
	wantCode(t, "minh moves it-hcm under hr", c.move(c.minh, "it-hcm", "hr"), apperr.ErrPermissionDenied)
	wantList(t, "parent after the denied move", c.parentOf(t, shop, "it-hcm"), []string{"it"})
	if n := c.audits(t, shop, "minh", "group_update"); n != 0 {
		t.Fatalf("audits of the denied move = %d; want 0", n)
	}
	must(t, "minh moves it-hcm under ops", c.move(c.minh, "it-hcm", "ops"))
	wantList(t, "parent after the allowed move", c.parentOf(t, shop, "it-hcm"), []string{"ops"})
}

func TestStoryOtherTenantIDsAreNotFound(t *testing.T) {
	ctx := context.Background()
	c := openStory(t)
	c.tenant(t, "mall")
	c.account(t, "mall", "mall-ha")
	c.group(t, "mall", "mall-only", "")
	c.role(t, "mall", "mall-role", "iam:machines.write")
	c.role(t, "mall", "it-admin", "iam:machines.write")
	calls := map[string]func() error{
		"GetAccount": func() error { _, err := c.svc.GetAccount(ctx, c.lan, "mall-ha"); return err },
		"GetRole":    func() error { _, err := c.svc.GetRole(ctx, c.lan, "mall-role"); return err },
		"AssignAccountRole of a foreign role": func() error {
			return c.svc.AssignAccountRole(ctx, c.lan, "ha", "mall-role")
		},
		"AssignAccountRole to a foreign account": func() error {
			return c.svc.AssignAccountRole(ctx, c.lan, "mall-ha", "role-reader")
		},
		"AssignGroupRole": func() error { return c.svc.AssignGroupRole(ctx, c.lan, "mall-only", "role-reader") },
		"UpdateGroup": func() error {
			_, err := c.svc.UpdateGroup(ctx, c.lan, "mall-only", store.GroupUpdate{Name: new("Renamed")})
			return err
		},
		"move under a foreign parent": func() error { return c.move(c.lan, "it-hcm", "mall-only") },
		"DeleteGroup":                 func() error { return c.svc.DeleteGroup(ctx, c.lan, "mall-only") },
		"DeleteRole":                  func() error { _, err := c.svc.DeleteRole(ctx, c.lan, "mall-role"); return err },
		"SetAccountGroups":            func() error { return c.svc.SetAccountGroups(ctx, c.lan, "ha", []string{"mall-only"}) },
	}
	for name, call := range calls {
		wantCode(t, name, call(), apperr.ErrNotFound)
	}
	r, err := c.svc.GetRole(ctx, c.lan, "it-admin")
	must(t, "GetRole it-admin", err)
	wantList(t, "shop it-admin permissions", r.Permissions, itAdminPermissions)
	wantList(t, "mall it-admin permissions", c.strings(t,
		"SELECT permission FROM role_permissions WHERE tenant_id = 'mall' AND role_id = 'it-admin'"), []string{"iam:machines.write"})
	wantList(t, "mall bindings", c.strings(t,
		"SELECT group_id FROM group_roles WHERE tenant_id = 'mall' UNION ALL SELECT account_id FROM account_roles WHERE tenant_id = 'mall'"), nil)
	wantList(t, "parent of it-hcm", c.parentOf(t, shop, "it-hcm"), []string{"it"})
	wantList(t, "groups of ha", c.groupsOf(t, shop, "ha"), nil)
}

func TestStoryUnassignTakesEffectOnTheNextCall(t *testing.T) {
	ctx := context.Background()
	c := openStory(t)
	must(t, "binh lists groups", c.listGroups(c.binh))
	must(t, "lan unbinds it-admin from it", c.svc.UnassignGroupRole(ctx, c.lan, "it", "it-admin"))
	wantCode(t, "binh lists groups next", c.listGroups(c.binh), apperr.ErrPermissionDenied)
	wantCode(t, "binh sets ha's groups next", c.svc.SetAccountGroups(ctx, c.binh, "ha", []string{"it-hcm"}), apperr.ErrPermissionDenied)
}

type pauseBeforeLock struct {
	store.AdminRunner
	reached chan struct{}
	resume  chan struct{}
}

type pausedTx struct {
	store.AdminTx
	p pauseBeforeLock
}

func (p pauseBeforeLock) RunAdmin(ctx context.Context, opts store.AdminTxOptions, fn func(store.AdminTx) error) error {
	return p.AdminRunner.RunAdmin(ctx, opts, func(tx store.AdminTx) error {
		return fn(pausedTx{AdminTx: tx, p: p})
	})
}

func (tx pausedTx) LockAccount(ctx context.Context, tenantID, id string) error {
	close(tx.p.reached)
	<-tx.p.resume
	return tx.AdminTx.LockAccount(ctx, tenantID, id)
}

func TestStoryRaceThenRetryIsBlocked(t *testing.T) {
	ctx := context.Background()
	c := openStory(t)
	pause := pauseBeforeLock{AdminRunner: c.store, reached: make(chan struct{}), resume: make(chan struct{})}
	paused := serviceAt(pause, storyStart.Add(time.Hour))
	first := make(chan error, 1)
	go func() { first <- paused.SetAccountGroups(ctx, c.binh, "ha", []string{"it-hcm"}) }()
	select {
	case <-pause.reached:
	case err := <-first:
		t.Fatalf("binh ended before the pause: %v", err)
	}
	minhErr := c.svc.AssignAccountRole(ctx, c.minh, "ha", "role-reader")
	close(pause.resume)
	binhErr := <-first
	must(t, "minh assigns role-reader to ha", minhErr)
	wantCode(t, "binh's crossed write", binhErr, apperr.ErrUnavailable)
	if !strings.Contains(binhErr.Error(), "40001") {
		t.Fatalf("binh's crossed write = %v; want SQLSTATE 40001", binhErr)
	}
	wantList(t, "groups of ha after the conflict", c.groupsOf(t, shop, "ha"), nil)
	if n := c.audits(t, shop, "binh", "account_groups_set"); n != 0 {
		t.Fatalf("audits of the failed write = %d; want 0", n)
	}
	wantCode(t, "binh's retry", c.svc.SetAccountGroups(ctx, c.binh, "ha", []string{"it-hcm"}), apperr.ErrPermissionDenied)
	wantList(t, "groups of ha after the retry", c.groupsOf(t, shop, "ha"), nil)
	wantList(t, "roles of ha", c.strings(t, "SELECT role_id FROM account_roles WHERE tenant_id = $1 AND account_id = 'ha'", shop), []string{"role-reader"})
}

func TestStoryExpiredSessionIsUnauthenticated(t *testing.T) {
	ctx := context.Background()
	c := openStory(t)
	idleEnd := storyStart.Add(storyIdle)
	live := serviceAt(c.store, idleEnd.Add(-1))
	_, _, err := live.ListGroups(ctx, c.lan, "")
	must(t, "lan lists groups just before the idle end", err)
	expired := serviceAt(c.store, idleEnd)
	_, _, err = expired.ListGroups(ctx, c.lan, "")
	wantCode(t, "read at the idle end", err, apperr.ErrUnauthenticated)
	err = expired.SetAccountGroups(ctx, c.lan, "ha", []string{"ops"})
	wantCode(t, "write at the idle end", err, apperr.ErrUnauthenticated)
	wantList(t, "groups of ha", c.groupsOf(t, shop, "ha"), nil)
	if n := c.audits(t, shop, "lan", "account_groups_set"); n != 0 {
		t.Fatalf("audits by an expired session = %d; want 0", n)
	}
}

func TestStoryLastTenantAdminMayRemoveItself(t *testing.T) {
	ctx := context.Background()
	c := openStory(t)
	must(t, "lan removes her own tenant-admin", c.svc.UnassignAccountRole(ctx, c.lan, "lan", "tenant-admin"))
	holders := c.strings(t, `SELECT account_id FROM account_roles WHERE tenant_id = $1 AND role_id = 'tenant-admin'
		UNION ALL SELECT ag.account_id FROM account_groups ag JOIN group_roles gr ON gr.tenant_id = ag.tenant_id AND gr.group_id = ag.group_id
		WHERE ag.tenant_id = $1 AND gr.role_id = 'tenant-admin'`, shop)
	wantList(t, "tenant-admin holders", holders, nil)
	if n := c.audits(t, shop, "lan", "account_role_unassign"); n != 1 {
		t.Fatalf("unassign audits = %d; want 1", n)
	}
	wantCode(t, "lan lists groups afterwards", c.listGroups(c.lan), apperr.ErrPermissionDenied)
}

func TestStoryUnknownPermissionIsInvalid(t *testing.T) {
	ctx := context.Background()
	c := openStory(t)
	typo := []string{"iam:acounts.read"}
	_, err := c.svc.CreateRole(ctx, c.lan, manage.NewRole{ID: "auditor", Name: "Auditor", Permissions: typo})
	wantCode(t, "CreateRole with a typo", err, apperr.ErrInvalidArgument)
	if !strings.Contains(err.Error(), "iam:acounts.read") {
		t.Fatalf("CreateRole error = %v; want it to name the typo", err)
	}
	_, err = c.svc.GetRole(ctx, c.lan, "auditor")
	wantCode(t, "role after the typo", err, apperr.ErrNotFound)
	_, err = c.svc.UpdateRole(ctx, c.lan, "role-reader", store.RoleUpdate{Permissions: &typo})
	wantCode(t, "UpdateRole with a typo", err, apperr.ErrInvalidArgument)
	r, err := c.svc.GetRole(ctx, c.lan, "role-reader")
	must(t, "GetRole role-reader", err)
	wantList(t, "role-reader permissions", r.Permissions, []string{"iam:roles.read"})
	if n := c.audits(t, shop, "lan", "role_create") + c.audits(t, shop, "lan", "role_update"); n != 0 {
		t.Fatalf("role audits = %d; want 0", n)
	}
}

func TestStoryWriteWithoutRead(t *testing.T) {
	ctx := context.Background()
	c := openStory(t)
	c.role(t, shop, "group-writer", "iam:groups.write")
	khoa := c.account(t, shop, "khoa")
	c.accountRoles(t, shop, "khoa", "group-writer")
	g, err := c.svc.CreateGroup(ctx, khoa, manage.NewGroup{ID: "kho", Name: "Kho"})
	must(t, "khoa creates a root group", err)
	if g.ID != "kho" || g.ParentID != "" {
		t.Fatalf("created group = %+v; want root kho", g)
	}
	wantCode(t, "khoa lists groups", c.listGroups(khoa), apperr.ErrPermissionDenied)
	if !slices.Contains(c.strings(t, "SELECT id FROM groups WHERE tenant_id = $1", shop), "kho") {
		t.Fatal("kho was not stored")
	}
}
