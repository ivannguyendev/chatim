package manage_test

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const tenant = "t1"

var now = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

var writeCalls = []string{
	"CreateRole", "UpdateRole", "DeleteRole", "AssignAccountRole", "UnassignAccountRole",
	"AssignGroupRole", "UnassignGroupRole", "CreateGroup", "UpdateGroup", "DeleteGroup", "SetAccountGroups", "Audit",
}

type world struct {
	sessions     map[string]bool
	grants       map[string]store.Grant
	accounts     map[string]store.AccountView
	roles        map[string]store.Role
	groups       map[string]store.GroupNode
	groupRoles   map[string][]string
	accountRoles map[string]bool
	groupBinds   map[string]bool
	owner        map[string]string
	deletedRoles int64
	fail         map[string]error
}

type fakeRunner struct {
	w         *world
	runs      []store.AdminTxOptions
	calls     []string
	audits    []store.AuditEvent
	pages     []string
	rollbacks int
}

type fakeTx struct {
	r        *fakeRunner
	readOnly bool
	audits   []store.AuditEvent
}

func perms(ps ...string) store.Grant { return store.Grant{Permissions: ps} }

func allIAMExcept(skip ...string) store.Grant {
	var out []string
	for _, p := range authz.IAMPermissions {
		if !slices.Contains(skip, p) {
			out = append(out, p)
		}
	}
	return perms(out...)
}

func role(id string, builtin bool, ps ...string) store.Role {
	return store.Role{TenantID: tenant, ID: id, Name: id, Builtin: builtin, Ver: 1, Permissions: ps}
}

func group(id string, ancestors ...string) store.GroupNode {
	parent := ""
	if len(ancestors) > 0 {
		parent = ancestors[len(ancestors)-1]
	}
	return store.GroupNode{Group: store.Group{TenantID: tenant, ID: id, Name: id, ParentID: parent, Mentionable: true, State: store.GroupLive}, Ancestors: ancestors}
}

func newWorld() *world {
	itAdmin := []string{"iam:groups.read", "iam:groups.write", "iam:groups.members.write", "iam:roles.assign", "iam:accounts.read"}
	w := &world{
		sessions: map[string]bool{"minh|s1": true, "binh|s2": true, "lan|s3": true},
		grants: map[string]store.Grant{
			"minh": allIAMExcept(),
			"ha":   perms("iam:groups.read"),
		},
		accounts: map[string]store.AccountView{},
		roles: map[string]store.Role{
			authz.TenantAdminRole: role(authz.TenantAdminRole, true),
			"it-admin":            role("it-admin", false, itAdmin...),
			"hr-admin":            role("hr-admin", false, "iam:roles.write"),
			"reader":              role("reader", false, "iam:groups.read"),
		},
		groups: map[string]store.GroupNode{},
		groupRoles: map[string][]string{
			"admins":   {authz.TenantAdminRole},
			"it":       {"it-admin"},
			"hr":       {"hr-admin"},
			"ops-root": {authz.TenantAdminRole},
		},
		accountRoles: map[string]bool{"ha|it-admin": true},
		groupBinds:   map[string]bool{},
		owner:        map[string]string{"far": "t2", "far-group": "t2", "far-role": "t2"},
		deletedRoles: 2,
		fail:         map[string]error{},
	}
	w.roles["far-role"] = role("far-role", false)
	for _, id := range []string{"minh", "binh", "ha", "lan", "far"} {
		w.accounts[id] = store.AccountView{ID: id, Username: id, DisplayName: id, State: store.StateActive, GroupIDs: []string{"it"}, RoleIDs: []string{"reader"}}
	}
	for _, g := range []store.GroupNode{
		group("cong-ty"), group("it", "cong-ty"), group("it-hcm", "cong-ty", "it"), group("hr"),
		group("admins"), group("ops-root"), group("ops", "ops-root"), group("far-group"),
	} {
		w.groups[g.ID] = g
	}
	for g, roles := range w.groupRoles {
		for _, rid := range roles {
			w.groupBinds[g+"|"+rid] = true
		}
	}
	return w
}

func newService(w *world) (*manage.Service, *fakeRunner) {
	r := &fakeRunner{w: w}
	return manage.New(manage.Deps{Store: r, Now: func() time.Time { return now }}), r
}

func caller(account string) auth.Caller {
	sessions := map[string]string{"minh": "s1", "binh": "s2", "lan": "s3"}
	return auth.Caller{Tenant: tenant, Account: account, Session: sessions[account], IP: "10.0.0.9"}
}

func (r *fakeRunner) RunAdmin(ctx context.Context, opts store.AdminTxOptions, fn func(store.AdminTx) error) error {
	r.runs = append(r.runs, opts)
	tx := &fakeTx{r: r, readOnly: opts.ReadOnly}
	if err := fn(tx); err != nil {
		r.rollbacks++
		return err
	}
	r.audits = append(r.audits, tx.audits...)
	return nil
}

func (r *fakeRunner) wrote() []string {
	var out []string
	for _, c := range r.calls {
		if slices.Contains(writeCalls, c) {
			out = append(out, c)
		}
	}
	return out
}

func (t *fakeTx) call(name string) error {
	t.r.calls = append(t.r.calls, name)
	if t.readOnly && slices.Contains(writeCalls, name) {
		return fmt.Errorf("%w: write %s in a read-only transaction", apperr.ErrUnavailable, name)
	}
	return t.r.w.fail[name]
}

func (w *world) here(tenantID, id string) bool {
	owner, ok := w.owner[id]
	if !ok {
		owner = tenant
	}
	return owner == tenantID
}

func notFound(what string) error { return fmt.Errorf("%w: %s", apperr.ErrNotFound, what) }

func (t *fakeTx) RunningSession(_ context.Context, c store.SessionCheck) error {
	if err := t.call("RunningSession"); err != nil {
		return err
	}
	if c.TenantID != tenant || !c.Now.Equal(now) || !t.r.w.sessions[c.AccountID+"|"+c.SessionID] {
		return apperr.ErrNotFound
	}
	return nil
}

func (t *fakeTx) AccountGrant(_ context.Context, _ string, id string) (store.Grant, error) {
	if err := t.call("AccountGrant"); err != nil {
		return store.Grant{}, err
	}
	return t.r.w.grants[id], nil
}

func (t *fakeTx) GroupsGrant(_ context.Context, tenantID string, ids []string) (store.Grant, error) {
	if err := t.call("GroupsGrant"); err != nil {
		return store.Grant{}, err
	}
	var out store.Grant
	for _, id := range ids {
		node, ok := t.r.w.groups[id]
		if !ok || !t.r.w.here(tenantID, id) {
			continue
		}
		for _, g := range append(slices.Clone(node.Ancestors), id) {
			for _, rid := range t.r.w.groupRoles[g] {
				out = merge(out, t.r.w.roleGrant(rid))
			}
		}
	}
	return out, nil
}

func merge(a, b store.Grant) store.Grant {
	return store.Grant{TenantAdmin: a.TenantAdmin || b.TenantAdmin, Permissions: append(slices.Clone(a.Permissions), b.Permissions...)}
}

func (w *world) roleGrant(id string) store.Grant {
	r := w.roles[id]
	return store.Grant{TenantAdmin: r.Builtin && r.ID == authz.TenantAdminRole, Permissions: r.Permissions}
}

func (t *fakeTx) LockAccount(_ context.Context, tenantID, id string) error {
	if err := t.call("LockAccount"); err != nil {
		return err
	}
	if _, ok := t.r.w.accounts[id]; !ok || !t.r.w.here(tenantID, id) {
		return notFound("account")
	}
	return nil
}

func (t *fakeTx) GroupNode(_ context.Context, tenantID, id string) (store.GroupNode, error) {
	if err := t.call("GroupNode"); err != nil {
		return store.GroupNode{}, err
	}
	n, ok := t.r.w.groups[id]
	if !ok || !t.r.w.here(tenantID, id) {
		return store.GroupNode{}, notFound("group")
	}
	return n, nil
}

func (t *fakeTx) CreateGroup(context.Context, store.Group) error { return t.call("CreateGroup") }

func (t *fakeTx) UpdateGroup(context.Context, string, string, store.GroupUpdate, time.Time, string) error {
	return t.call("UpdateGroup")
}

func (t *fakeTx) DeleteGroup(context.Context, string, string, time.Time, string) (int64, error) {
	return t.r.w.deletedRoles, t.call("DeleteGroup")
}

func (t *fakeTx) SetAccountGroups(_ context.Context, tenantID, _ string, ids []string) error {
	if err := t.call("SetAccountGroups"); err != nil {
		return err
	}
	for _, id := range ids {
		if _, ok := t.r.w.groups[id]; !ok || !t.r.w.here(tenantID, id) {
			return notFound("group")
		}
	}
	return nil
}

func (t *fakeTx) Audit(_ context.Context, a store.AuditEvent) error {
	if err := t.call("Audit"); err != nil {
		return err
	}
	t.audits = append(t.audits, a)
	return nil
}
