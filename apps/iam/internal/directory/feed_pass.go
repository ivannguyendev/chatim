package directory

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/backoff"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

const (
	feedPageSize = 1000
	backoffBase  = time.Second
	backoffCap   = 30 * time.Second
)

type RevocationSource interface {
	Revocations(ctx context.Context) (revocation.Snapshot, error)
}

type feedPass struct {
	next    store.Position
	read    int
	pending bool
}

func drainFeed(ctx context.Context, r store.DirectoryReader, c *Cache, from store.Position) (feedPass, error) {
	pass := feedPass{next: from}
	for {
		page, err := r.Changes(ctx, pass.next, feedPageSize)
		if err != nil {
			return pass, err
		}
		if len(page.Changes) == 0 {
			pass.pending = page.PendingAboveHorizon
			return pass, nil
		}
		c.Apply(page.Changes)
		c.SetPosition(page.Next)
		pass.next = page.Next
		pass.read += len(page.Changes)
	}
}

func refreshRevocations(ctx context.Context, src RevocationSource, list *revocation.List) error {
	snap, err := src.Revocations(ctx)
	now := time.Now()
	if err == nil {
		list.Merge(snap, now)
	}
	list.Prune(now)
	return err
}

func retryDelay(failures int) time.Duration {
	d := backoffCap
	if failures < 5 {
		d = min(backoffBase<<failures, backoffCap)
	}
	return backoff.Between(d*8/10, d*12/10)
}
