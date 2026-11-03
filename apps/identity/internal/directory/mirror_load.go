package directory

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/backoff"
)

const (
	loadSlotPurpose = "directory_load"
	slotRenewEvery  = 10 * time.Second
)

var errLoadSlotLost = errors.New("directory load slot lost")

func (m *Mirror) load(ctx context.Context, run *mirrorRun) {
	began := time.Now()
	loaded, err := m.loadPass(ctx, run)
	if ctx.Err() != nil {
		return
	}
	var wait time.Duration
	switch {
	case err != nil:
		m.Stats.loadFailures.Add(1)
		wait = retryDelay(run.loadFailures)
		run.loadFailures++
	case !loaded:
		wait = backoff.Between(slotWaitMin, slotWaitMax)
	default:
		run.loadFailures = 0
		m.Stats.lastLoad.Store(int64(time.Since(began)))
		if m.OnLoaded != nil {
			m.OnLoaded()
		}
		return
	}
	m.pause(ctx, run, wait)
}

func (m *Mirror) loadPass(ctx context.Context, run *mirrorRun) (bool, error) {
	slot, ok, err := m.Source.TryLoadSlot(ctx, loadSlotPurpose, m.Slots)
	if err != nil || !ok {
		return false, err
	}
	defer slot.Release()
	start, err := m.Source.StreamStart(ctx)
	if err != nil {
		return false, err
	}
	users, err := m.loadUsers(ctx, slot)
	if err != nil {
		return false, err
	}
	if err := refreshRevocations(ctx, m.Source, m.List); err != nil {
		return false, err
	}
	run.recheckAt = time.Now().Add(m.Recheck)
	m.Cache.Replace(users, start)
	return true, nil
}

func (m *Mirror) loadUsers(ctx context.Context, slot store.Slot) (map[store.UserKey]Entry, error) {
	users := map[store.UserKey]Entry{}
	renewedAt := time.Now()
	var after store.UserKey
	for {
		entries, err := m.Source.LoadPage(ctx, after, loadPageSize)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			putEntry(users, e)
		}
		if len(entries) < loadPageSize {
			return users, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Since(renewedAt) >= slotRenewEvery {
			held, err := slot.Renew(ctx)
			if err != nil {
				return nil, err
			}
			if !held {
				return nil, errLoadSlotLost
			}
			renewedAt = time.Now()
		}
		last := entries[len(entries)-1]
		after = store.UserKey{TenantID: last.TenantID, ID: last.ID}
	}
}
