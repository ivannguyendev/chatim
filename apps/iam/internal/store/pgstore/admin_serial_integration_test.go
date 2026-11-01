package pgstore_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type adminSettings struct {
	isolation, readOnly, lockTimeout, statementTimeout string
}

func readAdminSettings(t *testing.T, s fixture, readOnly bool) (adminSettings, error, error) {
	t.Helper()
	ctx := context.Background()
	var got adminSettings
	var writeErr error
	err := s.RunAdminTx(ctx, "", readOnly, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			"SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_setting('lock_timeout'), current_setting('statement_timeout')").
			Scan(&got.isolation, &got.readOnly, &got.lockTimeout, &got.statementTimeout)
		if err != nil {
			return err
		}
		_, writeErr = tx.Exec(ctx, "INSERT INTO clients (id, name, created_at, updated_at, updated_by) VALUES ('probe', 'Probe', now(), now(), 'test')")
		return writeErr
	})
	return got, writeErr, err
}

func TestAdminWritesRunSerializable(t *testing.T) {
	s := openStore(t)
	got, writeErr, err := readAdminSettings(t, s, false)
	if err != nil || writeErr != nil {
		t.Fatalf("write tx = %v, insert = %v", err, writeErr)
	}
	if want := (adminSettings{"serializable", "off", "5s", "5s"}); got != want {
		t.Fatalf("write tx settings = %+v; want %+v", got, want)
	}
	got, writeErr, err = readAdminSettings(t, s, true)
	if want := (adminSettings{"repeatable read", "on", "5s", "5s"}); got != want {
		t.Fatalf("read-only tx settings = %+v; want %+v", got, want)
	}
	var pgErr *pgconn.PgError
	if !errors.As(writeErr, &pgErr) || pgErr.Code != "25006" {
		t.Fatalf("insert in a read-only tx = %v; want 25006", writeErr)
	}
	wantErr(t, err, apperr.ErrUnavailable, "read-only tx that writes")
	if n := len(queryStrings(t, s.raw, "SELECT id FROM clients WHERE id = 'probe'")); n != 1 {
		t.Fatalf("probe clients = %d; want 1 (written once by the write tx)", n)
	}
}

func treeLocks(t *testing.T, s fixture) int {
	t.Helper()
	var n int
	err := s.raw.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = 2 AND objsubid = 2 AND database = (SELECT oid FROM pg_database WHERE datname = current_database())").Scan(&n)
	if err != nil {
		t.Fatalf("count tree locks: %v", err)
	}
	return n
}

func backendPID(t *testing.T, st *pgstore.Store) uint32 {
	t.Helper()
	ctx := context.Background()
	var pid uint32
	if err := st.RunAdminTx(ctx, "", false, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid)
	}); err != nil {
		t.Fatalf("read backend pid: %v", err)
	}
	return pid
}

func panicInTreeLockedTx(ctx context.Context, st *pgstore.Store) (err error) {
	defer func() {
		if recover() == nil {
			err = errors.New("fn did not panic")
		}
	}()
	return st.RunAdminTx(ctx, "shop", false, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO groups (tenant_id, id, name, state, created_at, updated_at, updated_by) VALUES ('shop', 'panic', 'Panic', 1, now(), now(), 'test')"); err != nil {
			return err
		}
		panic("boom")
	})
}

func TestTreeLockIsReleasedAfterCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	createGroups(t, s, group("a", ""), group("b", "a"))
	one, err := pgstore.Open(ctx, s.uri, pgstore.Options{MaxConns: 1, RevocationKeep: testKeep})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(one.Close)
	pid := backendPID(t, one)
	steps := []struct {
		name string
		run  func() error
		want error
	}{
		{"create under a parent commits", func() error { return one.CreateGroup(ctx, group("c", "a"), audit("group_create")) }, nil},
		{"move into a cycle rolls back", func() error {
			return one.UpdateGroup(ctx, "shop", "a", moveUnder("c"), audit("group_update"))
		}, apperr.ErrFailedPrecondition},
		{"delete with children rolls back", func() error { return one.DeleteGroup(ctx, "shop", "a", audit("group_delete")) }, apperr.ErrFailedPrecondition},
		{"delete commits", func() error { return one.DeleteGroup(ctx, "shop", "c", audit("group_delete")) }, nil},
		{"panic rolls back", func() error { return panicInTreeLockedTx(ctx, one) }, nil},
	}
	for _, step := range steps {
		wantErr(t, step.run(), step.want, step.name)
		if n := treeLocks(t, s); n != 0 {
			t.Fatalf("%s: %d tree locks left", step.name, n)
		}
		if got := backendPID(t, one); got != pid {
			t.Fatalf("%s: backend pid = %d; want %d (connection released, not dropped)", step.name, got, pid)
		}
	}
	if n := len(queryStrings(t, s.raw, "SELECT id FROM groups WHERE id = 'panic'")); n != 0 {
		t.Fatal("the panicking tx committed its group")
	}
}

func TestAdminWaitsAreBoundedAndUnavailable(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		run   func(t *testing.T, s fixture) error
		codes []string
	}{
		{"row lock wait", func(t *testing.T, s fixture) error {
			g := groupRowGate(t, s, "sales")
			defer g.open()
			return s.UpdateGroup(ctx, "shop", "sales", store.GroupUpdate{Name: new("Sales")}, audit("group_update"))
		}, []string{"55P03", "57014"}},
		{"long statement", func(t *testing.T, s fixture) error {
			return s.RunAdminTx(ctx, "", false, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, "SELECT pg_sleep(30)")
				return err
			})
		}, []string{"57014"}},
		{"tree lock wait", func(t *testing.T, s fixture) error {
			g := treeGate(t, s)
			defer g.open()
			return s.CreateGroup(ctx, group("sales-hn", "sales"), audit("group_create"))
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openStore(t)
			seedTenant(t, s, "shop")
			createGroups(t, s, group("sales", ""))
			err := tc.run(t, s)
			wantErr(t, err, apperr.ErrUnavailable, tc.name)
			if len(tc.codes) > 0 && !slices.ContainsFunc(tc.codes, func(code string) bool { return strings.Contains(err.Error(), code) }) {
				t.Fatalf("%s: err = %v; want one of %v", tc.name, err, tc.codes)
			}
		})
	}
}

func TestMovingUnderADeepDescendantIsRefused(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	createGroups(t, s, group("g", ""), group("child", "g"), group("grandchild", "child"), group("great", "grandchild"))
	wantErr(t, s.UpdateGroup(ctx, "shop", "g", moveUnder("great"), audit("group_update")), apperr.ErrFailedPrecondition, "move g under great")
	wantAncestors(t, s.raw, map[string][]string{"g": {}, "great": {"g", "child", "grandchild"}})
	if got := readGroup(t, s, "g").ParentID; got != "" {
		t.Fatalf("parent of g = %q; want root", got)
	}
	wantAudits(t, s, "group_update", 0)
}

func TestDeletingAGroupDropsItsRoleBindings(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	createGroups(t, s, group("sales", ""), group("support", ""))
	exec(t, s.raw, "INSERT INTO roles (tenant_id, id, name, builtin, ver, created_at, updated_at, updated_by) VALUES ('shop', 'viewer', 'Viewer', false, 1, now(), now(), 'test')")
	exec(t, s.raw, `INSERT INTO group_roles (tenant_id, group_id, role_id, created_at, created_by) VALUES
		('shop', 'sales', 'tenant-admin', now(), 'test'), ('shop', 'sales', 'viewer', now(), 'test'), ('shop', 'support', 'viewer', now(), 'test')`)
	at := audit("group_delete")
	if err := s.DeleteGroup(ctx, "shop", "sales", at); err != nil {
		t.Fatalf("DeleteGroup sales: %v", err)
	}
	if _, ok := at.Detail["roles"]; ok {
		t.Fatal("DeleteGroup changed the caller's audit detail")
	}
	bindings := queryStrings(t, s.raw, "SELECT group_id || '/' || role_id FROM group_roles WHERE tenant_id = 'shop' ORDER BY 1")
	if want := []string{"support/viewer"}; !slices.Equal(bindings, want) {
		t.Fatalf("group_roles = %v; want %v", bindings, want)
	}
	if err := s.DeleteGroup(ctx, "shop", "support", audit("group_delete")); err != nil {
		t.Fatalf("DeleteGroup support: %v", err)
	}
	details := queryStrings(t, s.raw, "SELECT (detail->>'k') || ' roles=' || (detail->>'roles') FROM audit_events WHERE action = 'group_delete' ORDER BY id")
	if want := []string{"v roles=2", "v roles=1"}; !slices.Equal(details, want) {
		t.Fatalf("group_delete audit = %v; want %v", details, want)
	}
}
