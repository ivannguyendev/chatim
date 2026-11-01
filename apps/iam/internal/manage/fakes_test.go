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
	deletedRoles int64
	fail         map[string]error
}

type fakeRunner struct {
	w      *world
	runs   []store.AdminTxOptions
	calls  []string
	audits []store.AuditEvent
	pages  []string
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
		deletedRoles: 2,
		fail:         map[string]error{},
	}
	for _, id := range []string{"minh", "binh", "ha", "lan"} {
		w.accounts[id] = store.AccountView{ID: id, Username: id, DisplayName: id, State: store.StateActive, GroupIDs: []string{"it"}, RoleIDs: []string{"reader"}}
	}
	for _, g := range []store.GroupNode{
		group("cong-ty"), group("it", "cong-ty"), group("it-hcm", "cong-ty", "it"), group("hr"),
		group("admins"), group("ops-root"), group("ops", "ops-root"),
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

func (t *fakeTx) GroupsGrant(_ context.Context, _ string, ids []string) (store.Grant, error) {
	if err := t.call("GroupsGrant"); err != nil {
		return store.Grant{}, err
	}
	var out store.Grant
	for _, id := range ids {
		node, ok := t.r.w.groups[id]
		if !ok {
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

func (t *fakeTx) LockAccount(_ context.Context, _ string, id string) error {
	if err := t.call("LockAccount"); err != nil {
		return err
	}
	if _, ok := t.r.w.accounts[id]; !ok {
		return notFound("account")
	}
	return nil
}

func (t *fakeTx) GroupNode(_ context.Context, _ string, id string) (store.GroupNode, error) {
	if err := t.call("GroupNode"); err != nil {
		return store.GroupNode{}, err
	}
	n, ok := t.r.w.groups[id]
	if !ok {
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

func (t *fakeTx) SetAccountGroups(context.Context, string, string, []string) error {
	return t.call("SetAccountGroups")
}

func (t *fakeTx) Audit(_ context.Context, a store.AuditEvent) error {
	if err := t.call("Audit"); err != nil {
		return err
	}
	t.audits = append(t.audits, a)
	return nil
}

func (t *fakeTx) CreateRole(context.Context, store.Role) error { return t.call("CreateRole") }

func (t *fakeTx) UpdateRole(_ context.Context, _ string, id string, u store.RoleUpdate, _ time.Time, _ string) (store.Role, []string, error) {
	if err := t.call("UpdateRole"); err != nil {
		return store.Role{}, nil, err
	}
	r, ok := t.r.w.roles[id]
	if !ok {
		return store.Role{}, nil, notFound("role")
	}
	if r.Builtin {
		return store.Role{}, nil, fmt.Errorf("%w: builtin role cannot change", apperr.ErrFailedPrecondition)
	}
	added := []string{}
	if u.Permissions != nil {
		for _, p := range *u.Permissions {
			if !slices.Contains(r.Permissions, p) {
				added = append(added, p)
			}
		}
		r.Permissions = *u.Permissions
	}
	if u.Name != nil {
		r.Name = *u.Name
	}
	r.Ver++
	return r, added, nil
}

func (t *fakeTx) DeleteRole(_ context.Context, _ string, id string) (store.RoleRemoval, error) {
	if err := t.call("DeleteRole"); err != nil {
		return store.RoleRemoval{}, err
	}
	if _, ok := t.r.w.roles[id]; !ok {
		return store.RoleRemoval{}, notFound("role")
	}
	return store.RoleRemoval{Accounts: 3, Groups: 1}, nil
}

func (t *fakeTx) Role(_ context.Context, _ string, id string) (store.Role, error) {
	if err := t.call("Role"); err != nil {
		return store.Role{}, err
	}
	r, ok := t.r.w.roles[id]
	if !ok {
		return store.Role{}, notFound("role")
	}
	return r, nil
}

func (t *fakeTx) RoleGrant(_ context.Context, _ string, id string) (store.Grant, error) {
	if err := t.call("RoleGrant"); err != nil {
		return store.Grant{}, err
	}
	if _, ok := t.r.w.roles[id]; !ok {
		return store.Grant{}, notFound("role")
	}
	return t.r.w.roleGrant(id), nil
}

func (t *fakeTx) AssignAccountRole(_ context.Context, _, account, roleID string, _ time.Time, _ string) (bool, error) {
	return !t.r.w.accountRoles[account+"|"+roleID], t.call("AssignAccountRole")
}

func (t *fakeTx) UnassignAccountRole(_ context.Context, _, account, roleID string) (bool, error) {
	return t.r.w.accountRoles[account+"|"+roleID], t.call("UnassignAccountRole")
}

func (t *fakeTx) AssignGroupRole(_ context.Context, _, groupID, roleID string, _ time.Time, _ string) (bool, error) {
	return !t.r.w.groupBinds[groupID+"|"+roleID], t.call("AssignGroupRole")
}

func (t *fakeTx) UnassignGroupRole(_ context.Context, _, groupID, roleID string) (bool, error) {
	return t.r.w.groupBinds[groupID+"|"+roleID], t.call("UnassignGroupRole")
}

func (t *fakeTx) Account(_ context.Context, _ string, id string) (store.AccountView, error) {
	if err := t.call("Account"); err != nil {
		return store.AccountView{}, err
	}
	a, ok := t.r.w.accounts[id]
	if !ok {
		return store.AccountView{}, notFound("account")
	}
	return a, nil
}

func (t *fakeTx) Accounts(_ context.Context, _ string, after string, limit int) ([]store.AccountView, string, error) {
	t.r.pages = append(t.r.pages, fmt.Sprintf("accounts %s %d", after, limit))
	return []store.AccountView{t.r.w.accounts["ha"], t.r.w.accounts["lan"]}, "lan", t.call("Accounts")
}

func (t *fakeTx) Roles(_ context.Context, _ string, after string, limit int) ([]store.Role, string, error) {
	t.r.pages = append(t.r.pages, fmt.Sprintf("roles %s %d", after, limit))
	return []store.Role{t.r.w.roles["reader"]}, "", t.call("Roles")
}

func (t *fakeTx) Groups(_ context.Context, _ string, after string, limit int) ([]store.GroupView, string, error) {
	t.r.pages = append(t.r.pages, fmt.Sprintf("groups %s %d", after, limit))
	return []store.GroupView{{Group: t.r.w.groups["it"].Group, RoleIDs: []string{"it-admin"}}}, "it", t.call("Groups")
}
