package directory

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/backoff"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

const (
	loadPageSize = 5000
	slotWaitMin  = 500 * time.Millisecond
	slotWaitMax  = 2 * time.Second
)

type Loader struct {
	Reader      store.DirectoryReader
	Revocations RevocationSource
	Cache       *Cache
	List        *revocation.List
	Slots       int
	OnLoaded    func()
	Stats       *Stats
}

func (l *Loader) Run(ctx context.Context) error {
	failures := 0
	for {
		began := time.Now()
		loaded, err := l.attempt(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		var wait time.Duration
		switch {
		case err != nil:
			l.Stats.loadFailures.Add(1)
			wait = retryDelay(failures)
			failures++
		case !loaded:
			wait = backoff.Between(slotWaitMin, slotWaitMax)
		default:
			l.Cache.markReady()
			l.Stats.lastLoad.Store(int64(time.Since(began)))
			if l.OnLoaded != nil {
				l.OnLoaded()
			}
			return nil
		}
		if !backoff.Pause(ctx, wait) {
			return ctx.Err()
		}
	}
}

func (l *Loader) attempt(ctx context.Context) (bool, error) {
	release, ok, err := l.Reader.TryLoadSlot(ctx, l.Slots)
	if err != nil || !ok {
		return false, err
	}
	defer release()
	start, err := l.Reader.LoadStart(ctx)
	if err != nil {
		return false, err
	}
	if err := l.loadEntries(ctx); err != nil {
		return false, err
	}
	if err := refreshRevocations(ctx, l.Revocations, l.List); err != nil {
		return false, err
	}
	pass, err := drainFeed(ctx, l.Reader, l.Cache, start)
	if err != nil {
		return false, err
	}
	l.Cache.SetPosition(pass.next)
	return true, nil
}

func (l *Loader) loadEntries(ctx context.Context) error {
	var after store.AccountKey
	for {
		entries, err := l.Reader.LoadPage(ctx, after, loadPageSize)
		if err != nil {
			return err
		}
		l.Cache.ApplyEntries(entries)
		if len(entries) < loadPageSize {
			return nil
		}
		last := entries[len(entries)-1]
		after = store.AccountKey{TenantID: last.TenantID, ID: last.ID}
	}
}
