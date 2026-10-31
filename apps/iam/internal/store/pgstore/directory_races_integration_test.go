package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type gate struct {
	t  *testing.T
	s  fixture
	tx pgx.Tx
}

func openGate(t *testing.T, s fixture, sql string, args ...any) gate {
	t.Helper()
	ctx := context.Background()
	tx, err := connect(t, s.uri).Begin(ctx)
	if err != nil {
		t.Fatalf("Begin gate: %v", err)
	}
	exec(t, tx, sql, args...)
	return gate{t: t, s: s, tx: tx}
}

func treeGate(t *testing.T, s fixture) gate {
	return openGate(t, s, "SELECT pg_advisory_xact_lock(2, hashtext('shop'))")
}

func groupRowGate(t *testing.T, s fixture, id string) gate {
	return openGate(t, s, "SELECT 1 FROM groups WHERE tenant_id = 'shop' AND id = $1 FOR UPDATE", id)
}

func (g gate) queue(op func() error) <-chan error {
	g.t.Helper()
	before := g.lockWaiters()
	done := make(chan error, 1)
	go func() { done <- op() }()
	g.waitForWaiters(before + 1)
	return done
}

func (g gate) open() {
	g.t.Helper()
	if err := g.tx.Commit(context.Background()); err != nil {
		g.t.Fatalf("Commit gate: %v", err)
	}
}

func (g gate) lockWaiters() int {
	g.t.Helper()
	var n int
	err := g.s.raw.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&n)
	if err != nil {
		g.t.Fatalf("count lock waiters: %v", err)
	}
	return n
}

func (g gate) waitForWaiters(want int) {
	g.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for g.lockWaiters() < want {
		if time.Now().After(deadline) {
			g.t.Fatalf("lock waiters stayed below %d", want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func wantNoCycle(t *testing.T, s fixture) {
	t.Helper()
	var cyclic bool
	err := s.raw.QueryRow(context.Background(), `
		WITH RECURSIVE walk(start, id, depth) AS (
			SELECT id, parent_id, 1 FROM groups WHERE tenant_id = 'shop' AND parent_id IS NOT NULL
			UNION ALL
			SELECT w.start, g.parent_id, w.depth + 1 FROM walk w JOIN groups g ON g.tenant_id = 'shop' AND g.id = w.id
			WHERE g.parent_id IS NOT NULL AND w.id <> w.start AND w.depth < 100
		)
		SELECT EXISTS (SELECT 1 FROM walk WHERE id = start)`).Scan(&cyclic)
	if err != nil {
		t.Fatalf("walk tree: %v", err)
	}
	if cyclic {
		t.Fatal("the group tree has a cycle")
	}
}

func wantOneWinner(t *testing.T, first, second error, loser error) {
	t.Helper()
	switch {
	case first == nil && errors.Is(second, loser), second == nil && errors.Is(first, loser):
	default:
		t.Fatalf("results = %v, %v; want exactly one nil and one %v", first, second, loser)
	}
}

func TestConcurrentMovesNeverCreateACycle(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	createGroups(t, s, group("a", ""), group("b", ""))
	move := func(id, parent string) func() error {
		return func() error { return s.UpdateGroup(ctx, "shop", id, moveUnder(parent), audit("group_update")) }
	}
	g := treeGate(t, s)
	first := g.queue(move("a", "b"))
	second := g.queue(move("b", "a"))
	g.open()
	if err := <-first; err != nil {
		t.Fatalf("first move: %v", err)
	}
	wantErr(t, <-second, apperr.ErrFailedPrecondition, "second move")
	wantNoCycle(t, s)
	wantAudits(t, s, "group_update", 1)
	for range 20 {
		exec(t, s.raw, "UPDATE groups SET parent_id = NULL WHERE tenant_id = 'shop'")
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, op := range []func() error{move("a", "b"), move("b", "a")} {
			go func() {
				<-start
				results <- op()
			}()
		}
		close(start)
		wantOneWinner(t, <-results, <-results, apperr.ErrFailedPrecondition)
		wantNoCycle(t, s)
	}
}

func wantNoLiveChildUnderADeletedParent(t *testing.T, s fixture) {
	t.Helper()
	var orphans int
	err := s.raw.QueryRow(context.Background(),
		"SELECT count(*) FROM groups c JOIN groups p ON p.tenant_id = c.tenant_id AND p.id = c.parent_id WHERE c.state = 1 AND p.state = 2").Scan(&orphans)
	if err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("%d live groups under a deleted parent", orphans)
	}
}

func TestCreatingAChildWhileTheParentIsDeletedNeverLeavesAnOrphan(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name         string
		deleteFirst  bool
		createErr    error
		deleteErr    error
		parentStatus store.GroupState
	}{
		{"delete runs first", true, apperr.ErrNotFound, nil, store.GroupDeleted},
		{"create runs first", false, nil, apperr.ErrFailedPrecondition, store.GroupLive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openStore(t)
			seedTenant(t, s, "shop")
			createGroups(t, s, group("sales", ""))
			create := func() error { return s.CreateGroup(ctx, group("sales-hn", "sales"), audit("group_create")) }
			remove := func() error { return s.DeleteGroup(ctx, "shop", "sales", audit("group_delete")) }
			g := treeGate(t, s)
			var created, deleted <-chan error
			if tc.deleteFirst {
				deleted, created = g.queue(remove), g.queue(create)
			} else {
				created, deleted = g.queue(create), g.queue(remove)
			}
			g.open()
			createErr, deleteErr := <-created, <-deleted
			if !errors.Is(createErr, tc.createErr) || !errors.Is(deleteErr, tc.deleteErr) {
				t.Fatalf("create = %v, delete = %v; want %v, %v", createErr, deleteErr, tc.createErr, tc.deleteErr)
			}
			if got := readGroup(t, s, "sales").State; got != tc.parentStatus {
				t.Fatalf("parent state = %d; want %d", got, tc.parentStatus)
			}
			wantNoLiveChildUnderADeletedParent(t, s)
		})
	}
}

func wantNoMembershipInADeletedGroup(t *testing.T, s fixture) {
	t.Helper()
	var stale int
	err := s.raw.QueryRow(context.Background(),
		"SELECT count(*) FROM account_groups m JOIN groups g ON g.tenant_id = m.tenant_id AND g.id = m.group_id WHERE g.state = 2").Scan(&stale)
	if err != nil {
		t.Fatalf("count stale memberships: %v", err)
	}
	if stale != 0 {
		t.Fatalf("%d memberships in a deleted group", stale)
	}
}

func TestSettingGroupsWhileTheGroupIsDeletedNeverKeepsTheMembership(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name        string
		deleteFirst bool
		setErr      error
		lanVer      int64
	}{
		{"set runs first", false, nil, 3},
		{"delete runs first", true, apperr.ErrNotFound, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openStore(t)
			seedTenant(t, s, "shop")
			seedAccount(t, s, "shop", lanID, "lan")
			createGroups(t, s, group("sales", ""))
			set := func() error {
				return s.SetAccountGroups(ctx, "shop", lanID, []string{"sales"}, audit("account_groups_set"))
			}
			remove := func() error { return s.DeleteGroup(ctx, "shop", "sales", audit("group_delete")) }
			g := groupRowGate(t, s, "sales")
			var setDone, deleted <-chan error
			if tc.deleteFirst {
				deleted, setDone = g.queue(remove), g.queue(set)
			} else {
				setDone, deleted = g.queue(set), g.queue(remove)
			}
			g.open()
			setErr, deleteErr := <-setDone, <-deleted
			if !errors.Is(setErr, tc.setErr) || deleteErr != nil {
				t.Fatalf("set = %v, delete = %v; want %v, nil", setErr, deleteErr, tc.setErr)
			}
			wantGroupsOf(t, s, lanID, nil, "after race")
			wantNoMembershipInADeletedGroup(t, s)
			wantVer(t, s, userKind, lanID, tc.lanVer, "after race")
		})
	}
}
