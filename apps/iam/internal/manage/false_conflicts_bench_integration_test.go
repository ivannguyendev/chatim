package manage_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	conflictAdmins      = 8
	conflictDescendants = 500
)

const conflictBranchSQL = `INSERT INTO groups (tenant_id, id, name, parent_id, state, created_at, updated_at, updated_by)
	SELECT 'shop', $1 || '-' || lpad(n::text, 4, '0'), 'G ' || n,
		CASE WHEN $3::int = 0 THEN $2 ELSE $2 || '-' || lpad((n % $3::int)::text, 4, '0') END, 1, now(), now(), 'test'
	FROM generate_series(0, $4::int - 1) n`

const conflictFillerSQL = `WITH filler AS (
	SELECT 'u-' || lpad(n::text, 5, '0') AS id FROM generate_series(0, $1::int - 1) n WHERE NOT ('u-' || lpad(n::text, 5, '0') = ANY($2::text[]))
), seeded AS (
	INSERT INTO accounts (tenant_id, id, username, display_name, state, password_hash, password_must_change, password_changed_at, ver, created_at, updated_at, updated_by)
	SELECT 'shop', id, id, id, 1, 'x', false, now(), 1, now(), now(), 'test' FROM filler
	RETURNING id
)
INSERT INTO account_groups (tenant_id, account_id, group_id) SELECT 'shop', id, 'teams' FROM seeded`

type conflictWorld struct {
	pgWorld
	mover   auth.Caller
	admins  []auth.Caller
	targets []string
}

func conflictTargets(accounts int) []string {
	spread := max(accounts/conflictAdmins, 1)
	out := make([]string, 0, conflictAdmins)
	for i := range conflictAdmins {
		out = append(out, fmt.Sprintf("u-%05d", i*spread+spread/2))
	}
	return out
}

func openConflictWorld(b *testing.B, accounts int) conflictWorld {
	b.Helper()
	ctx := context.Background()
	w := openWorld(b, 2*conflictAdmins+4, conflictAdmins+2)
	w.tenant(b, shop)
	cw := conflictWorld{pgWorld: w, mover: w.account(b, shop, "lan")}
	w.accountRoles(b, shop, "lan", "tenant-admin")
	w.role(b, shop, "members-admin", "iam:groups.members.write", "iam:groups.read")
	for _, g := range [][2]string{{"left", ""}, {"right", ""}, {"teams", ""}, {"big", "left"}} {
		w.group(b, shop, g[0], g[1])
	}
	for _, level := range []struct {
		prefix, parent string
		parents, count int
	}{{"big-a", "big", 0, 20}, {"big-b", "big-a", 20, 480}} {
		if _, err := w.raw.Exec(ctx, conflictBranchSQL, level.prefix, level.parent, level.parents, level.count); err != nil {
			b.Fatalf("seed %s: %v", level.prefix, err)
		}
	}
	if n := len(w.strings(b, "SELECT id FROM groups WHERE tenant_id = $1 AND 'big' = ANY(ancestors)", shop)); n != conflictDescendants {
		b.Fatalf("descendants of big = %d; want %d", n, conflictDescendants)
	}
	for i := range conflictAdmins {
		admin := fmt.Sprintf("admin-%d", i)
		cw.admins = append(cw.admins, w.account(b, shop, admin))
		w.accountRoles(b, shop, admin, "members-admin")
		w.group(b, shop, fmt.Sprintf("team-%d-a", i), "teams")
		w.group(b, shop, fmt.Sprintf("team-%d-b", i), "teams")
	}
	cw.targets = conflictTargets(accounts)
	for _, target := range cw.targets {
		w.account(b, shop, target)
	}
	if _, err := w.raw.Exec(ctx, conflictFillerSQL, accounts, cw.targets); err != nil {
		b.Fatalf("seed filler accounts: %v", err)
	}
	if _, err := w.raw.Exec(ctx, "ANALYZE"); err != nil {
		b.Fatalf("ANALYZE: %v", err)
	}
	return cw
}

type conflictTally struct {
	unavailable atomic.Int64
	writes      atomic.Int64
	mu          sync.Mutex
	failure     error
}

func (t *conflictTally) record(err error) {
	t.writes.Add(1)
	if errors.Is(err, apperr.ErrUnavailable) {
		t.unavailable.Add(1)
		return
	}
	if err != nil {
		t.mu.Lock()
		t.failure = err
		t.mu.Unlock()
	}
}

func (cw conflictWorld) adminRound(round int, tally *conflictTally) {
	suffix := [2]string{"a", "b"}[round%2]
	var wg sync.WaitGroup
	for i, admin := range cw.admins {
		wg.Go(func() {
			team := fmt.Sprintf("team-%d-%s", i, suffix)
			tally.record(cw.svc.SetAccountGroups(context.Background(), admin, cw.targets[i], []string{team}))
		})
	}
	wg.Wait()
}

func (cw conflictWorld) moveUntil(stop <-chan struct{}, tally *conflictTally) {
	parents := [2]string{"right", "left"}
	moved := 0
	for {
		select {
		case <-stop:
			return
		default:
		}
		parent := parents[moved%2]
		_, err := cw.svc.UpdateGroup(context.Background(), cw.mover, "big", store.GroupUpdate{ParentID: &parent})
		tally.record(err)
		if err == nil {
			moved++
		}
	}
}

func reportTally(b *testing.B, prefix string, tally *conflictTally) {
	b.Helper()
	if tally.failure != nil {
		b.Fatalf("%s write failed: %v", prefix, tally.failure)
	}
	writes, unavailable := tally.writes.Load(), tally.unavailable.Load()
	b.ReportMetric(float64(writes), prefix+"-writes")
	b.ReportMetric(float64(unavailable), prefix+"-unavailable")
	if writes > 0 {
		b.ReportMetric(100*float64(unavailable)/float64(writes), prefix+"-unavailable-%")
	}
}

func BenchmarkFalseConflictsAmongEightAdmins(b *testing.B) {
	for _, accounts := range []int{0, 10000} {
		b.Run(fmt.Sprintf("filler-accounts-%d", accounts), func(b *testing.B) {
			runConflictRounds(b, openConflictWorld(b, accounts))
		})
	}
}

func runConflictRounds(b *testing.B, cw conflictWorld) {
	for _, withMove := range []bool{false, true} {
		name := map[bool]string{false: "alone", true: "beside-a-500-descendant-move"}[withMove]
		b.Run(name, func(b *testing.B) {
			var admins, mover conflictTally
			stop, moverDone := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(moverDone)
				if withMove {
					cw.moveUntil(stop, &mover)
				}
			}()
			round := 0
			for b.Loop() {
				cw.adminRound(round, &admins)
				round++
			}
			close(stop)
			<-moverDone
			reportTally(b, "admin", &admins)
			if withMove {
				reportTally(b, "move", &mover)
			}
		})
	}
}
