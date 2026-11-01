package pgstore_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func sessionCheck(tenant, account, id string, now time.Time) store.SessionCheck {
	return store.SessionCheck{TenantID: tenant, AccountID: account, SessionID: id, Now: now}
}

func checkSession(s fixture, readOnly bool, c store.SessionCheck) error {
	ctx := context.Background()
	return s.RunAdmin(ctx, store.AdminTxOptions{ReadOnly: readOnly}, func(a store.AdminTx) error {
		return a.RunningSession(ctx, c)
	})
}

func TestRunningSessionRefusesExpiredRevokedAndForeign(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedTenant(t, s, "mall")
	binh := seedAccount(t, s, "shop", "binh", "binh")
	lan := seedAccount(t, s, "shop", "lan", "lan")
	mallBinh := seedAccount(t, s, "mall", "binh", "binh")
	seedSessions(t, s, binh, "s-live", "s-hard", "s-revoked")
	seedSessions(t, s, lan, "s-lan")
	seedSessions(t, s, mallBinh, "s-mall")
	hardEnd := t0.Add(time.Hour)
	exec(t, s.raw, "UPDATE sessions SET expires_at = $1 WHERE id = 's-hard'", hardEnd)
	if _, err := s.RevokeSession(ctx, "s-revoked", revocation(), audit("session_revoke")); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	idleEnd := newSession("s-live", binh).Session.IdleExpiresAt
	cases := []struct {
		name  string
		check store.SessionCheck
		want  error
	}{
		{"running", sessionCheck("shop", "binh", "s-live", t0), nil},
		{"at the idle end", sessionCheck("shop", "binh", "s-live", idleEnd), apperr.ErrNotFound},
		{"just before the hard end", sessionCheck("shop", "binh", "s-hard", hardEnd.Add(-time.Nanosecond)), nil},
		{"at the hard end", sessionCheck("shop", "binh", "s-hard", hardEnd), apperr.ErrNotFound},
		{"revoked", sessionCheck("shop", "binh", "s-revoked", t0), apperr.ErrNotFound},
		{"another account's session", sessionCheck("shop", "binh", "s-lan", t0), apperr.ErrNotFound},
		{"another tenant's session", sessionCheck("shop", "binh", "s-mall", t0), apperr.ErrNotFound},
		{"own session under another tenant", sessionCheck("mall", "binh", "s-live", t0), apperr.ErrNotFound},
	}
	for _, readOnly := range []bool{false, true} {
		for _, tc := range cases {
			step := fmt.Sprintf("%s (read only %v)", tc.name, readOnly)
			err := checkSession(s, readOnly, tc.check)
			if tc.want == nil && err != nil {
				t.Fatalf("%s: err = %v; want nil", step, err)
			}
			if tc.want != nil {
				wantErr(t, err, tc.want, step)
			}
		}
	}
	for _, readOnly := range []bool{false, true} {
		err := s.RunAdmin(ctx, store.AdminTxOptions{ReadOnly: readOnly}, func(a store.AdminTx) error {
			if err := a.RunningSession(ctx, sessionCheck("shop", "binh", "s-live", t0)); err != nil {
				return err
			}
			_, err := s.raw.Exec(ctx, "SELECT 1 FROM sessions WHERE id = 's-live' FOR UPDATE NOWAIT")
			var pgErr *pgconn.PgError
			locked := errors.As(err, &pgErr) && pgErr.Code == "55P03"
			if locked == readOnly || (!locked && err != nil) {
				t.Errorf("read only %v: concurrent FOR UPDATE NOWAIT = %v; want the row shared only by writes", readOnly, err)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("read only %v: RunAdmin: %v", readOnly, err)
		}
	}
}

func TestReadOnlyAdminTxRefusesWrites(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", "binh", "binh")
	createGroups(t, s, group("sales", ""))
	readOnly := store.AdminTxOptions{ReadOnly: true}
	err := s.RunAdmin(ctx, readOnly, func(a store.AdminTx) error {
		if err := a.LockAccount(ctx, "shop", "binh"); err != nil {
			return fmt.Errorf("LockAccount: %w", err)
		}
		if _, err := a.GroupNode(ctx, "shop", "sales"); err != nil {
			return fmt.Errorf("GroupNode: %w", err)
		}
		_, err := a.AccountGrant(ctx, "shop", "binh")
		return err
	})
	if err != nil {
		t.Fatalf("reads in a read-only tx: %v", err)
	}
	writes := map[string]func(a store.AdminTx) error{
		"create group": func(a store.AdminTx) error { return a.CreateGroup(ctx, group("support", "")) },
		"update group": func(a store.AdminTx) error {
			return a.UpdateGroup(ctx, "shop", "sales", store.GroupUpdate{Name: new("Renamed")}, t0, "test")
		},
		"delete group": func(a store.AdminTx) error {
			_, err := a.DeleteGroup(ctx, "shop", "sales", t0, "test")
			return err
		},
		"set account groups": func(a store.AdminTx) error { return a.SetAccountGroups(ctx, "shop", "binh", []string{"sales"}) },
		"audit":              func(a store.AdminTx) error { return a.Audit(ctx, audit("probe")) },
	}
	for name, write := range writes {
		var inner error
		err := s.RunAdmin(ctx, readOnly, func(a store.AdminTx) error {
			inner = write(a)
			return inner
		})
		wantErr(t, inner, apperr.ErrUnavailable, name)
		if !strings.Contains(inner.Error(), "25006") {
			t.Fatalf("%s: err = %v; want SQLSTATE 25006", name, inner)
		}
		wantErr(t, err, apperr.ErrUnavailable, name+" (RunAdmin)")
	}
	if g := readGroup(t, s, "sales"); g.Name != "Group sales" || g.State != store.GroupLive {
		t.Fatalf("sales = %+v; want unchanged", g)
	}
	if n := len(queryStrings(t, s.raw, "SELECT id FROM groups WHERE id = 'support'")); n != 0 {
		t.Fatal("a read-only tx created a group")
	}
	wantGroupsOf(t, s, "binh", nil, "after read-only writes")
	wantAudits(t, s, "probe", 0)
}

func TestAuditIsWrittenOnCommitOnly(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	denied := fmt.Errorf("%w: missing iam:groups.write", apperr.ErrPermissionDenied)
	for _, result := range []error{denied, nil} {
		err := s.RunAdmin(ctx, store.AdminTxOptions{}, func(a store.AdminTx) error {
			if err := a.CreateGroup(ctx, group("sales", "")); err != nil {
				return err
			}
			if err := a.Audit(ctx, audit("group_create")); err != nil {
				return err
			}
			return result
		})
		if !errors.Is(err, result) || (result == nil && err != nil) {
			t.Fatalf("RunAdmin = %v; want %v", err, result)
		}
		want := 0
		if result == nil {
			want = 1
		}
		wantAudits(t, s, "group_create", want)
		if n := len(queryStrings(t, s.raw, "SELECT id FROM groups WHERE id = 'sales'")); n != want {
			t.Fatalf("groups named sales = %d; want %d", n, want)
		}
	}
}

func TestRunAdminTakesTheTreeLockOnlyWhenAsked(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	for _, opts := range []store.AdminTxOptions{{Tenant: "shop"}, {Tenant: "shop", TreeLock: true}, {Tenant: "shop", TreeLock: true, ReadOnly: true}} {
		err := s.RunAdmin(ctx, opts, func(a store.AdminTx) error {
			want := 0
			if opts.TreeLock {
				want = 1
			}
			if n := treeLocks(t, s); n != want {
				t.Errorf("%+v: tree locks = %d; want %d", opts, n, want)
			}
			var readOnly string
			if err := pgstore.AdminTxConn(a).QueryRow(ctx, "SELECT current_setting('transaction_read_only')").Scan(&readOnly); err != nil {
				return err
			}
			if want := map[bool]string{false: "off", true: "on"}[opts.ReadOnly]; readOnly != want {
				t.Errorf("%+v: transaction_read_only = %s; want %s", opts, readOnly, want)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%+v: RunAdmin: %v", opts, err)
		}
	}
	called := false
	err := s.RunAdmin(ctx, store.AdminTxOptions{TreeLock: true}, func(store.AdminTx) error {
		called = true
		return nil
	})
	wantErr(t, err, apperr.ErrInvalidArgument, "tree lock without a tenant")
	if called {
		t.Fatal("RunAdmin ran fn without a tenant for the tree lock")
	}
}

func TestRunAdminWaitsForAFreeDBSlot(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	one, err := pgstore.Open(ctx, s.uri, pgstore.Options{MaxConns: 4, RevocationKeep: testKeep, AdminDBSlots: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(one.Close)
	entered, finish := make(chan struct{}), make(chan struct{})
	stop := sync.OnceFunc(func() { close(finish) })
	defer stop()
	var firstDone atomic.Bool
	first := make(chan error, 1)
	go func() {
		first <- one.RunAdmin(ctx, store.AdminTxOptions{}, func(store.AdminTx) error {
			close(entered)
			<-finish
			firstDone.Store(true)
			return nil
		})
	}()
	<-entered
	waitCtx, cancel := context.WithCancel(ctx)
	var cancelledRan atomic.Bool
	cancelled := make(chan error, 1)
	go func() {
		cancelled <- one.RunAdmin(waitCtx, store.AdminTxOptions{}, func(store.AdminTx) error {
			cancelledRan.Store(true)
			return nil
		})
	}()
	cancel()
	wantErr(t, <-cancelled, apperr.ErrUnavailable, "wait ended by the context")
	if cancelledRan.Load() {
		t.Fatal("a caller ran while the only slot was held")
	}
	next := make(chan error, 1)
	go func() {
		next <- one.RunAdmin(ctx, store.AdminTxOptions{}, func(store.AdminTx) error {
			if !firstDone.Load() {
				return errors.New("entered while the first transaction held the slot")
			}
			return nil
		})
	}()
	stop()
	if err := <-first; err != nil {
		t.Fatalf("first RunAdmin: %v", err)
	}
	if err := <-next; err != nil {
		t.Fatalf("next RunAdmin: %v", err)
	}
}

var errFirstWriterEnded = errors.New("the first writer ended before both read")

func TestCrossedAdminWritesConflict(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	for _, id := range []string{"minh", "binh", "ha"} {
		seedAccount(t, s, "shop", id, id)
	}
	createGroups(t, s, group("sales", ""))
	seedRole(t, s, "shop", "r", permGroupsRead)
	aRead, bRead := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(bRead) })
	defer release()
	aDone := make(chan error, 1)
	go func() {
		aDone <- s.RunAdmin(ctx, store.AdminTxOptions{Tenant: "shop"}, func(a store.AdminTx) error {
			if _, err := a.AccountGrant(ctx, "shop", "ha"); err != nil {
				return err
			}
			close(aRead)
			<-bRead
			if _, err := pgstore.AdminTxConn(a).Exec(ctx,
				"INSERT INTO account_roles (tenant_id, account_id, role_id, created_at, created_by) VALUES ('shop', 'ha', 'r', now(), 'minh')"); err != nil {
				return err
			}
			return a.Audit(ctx, audit("account_role_assign"))
		})
	}()
	var aErr error
	bErr := s.RunAdmin(ctx, store.AdminTxOptions{Tenant: "shop"}, func(b store.AdminTx) error {
		if _, err := b.AccountGrant(ctx, "shop", "ha"); err != nil {
			return err
		}
		select {
		case <-aRead:
		case aErr = <-aDone:
			return errFirstWriterEnded
		}
		release()
		if aErr = <-aDone; aErr != nil {
			return errFirstWriterEnded
		}
		if err := b.SetAccountGroups(ctx, "shop", "ha", []string{"sales"}); err != nil {
			return err
		}
		return b.Audit(ctx, audit("account_groups_set"))
	})
	if aErr != nil {
		t.Fatalf("Minh's assign: %v", aErr)
	}
	wantErr(t, bErr, apperr.ErrUnavailable, "Binh's set groups")
	if !strings.Contains(bErr.Error(), "40001") {
		t.Fatalf("Binh's set groups = %v; want SQLSTATE 40001", bErr)
	}
	if roles := queryStrings(t, s.raw, "SELECT role_id FROM account_roles WHERE tenant_id = 'shop' AND account_id = 'ha'"); len(roles) != 1 || roles[0] != "r" {
		t.Fatalf("roles of ha = %v; want [r]", roles)
	}
	wantGroupsOf(t, s, "ha", nil, "after the conflict")
	wantAudits(t, s, "account_role_assign", 1)
	wantAudits(t, s, "account_groups_set", 0)
}
