package directory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

const (
	recheckInterval      = 2 * time.Second
	listenerCloseTimeout = 2 * time.Second
)

type Follower struct {
	Listen      func(ctx context.Context) (store.Listener, error)
	Reader      store.DirectoryReader
	Revocations RevocationSource
	Cache       *Cache
	List        *revocation.List
	Stats       *Stats

	mu     sync.Mutex
	signal chan struct{}
}

type listenState struct {
	listener    store.Listener
	failures    int
	reconnectAt time.Time
}

func (f *Follower) Signal() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.signal == nil {
		f.signal = make(chan struct{})
	}
	return f.signal
}

func (f *Follower) broadcast() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.signal != nil {
		close(f.signal)
	}
	f.signal = make(chan struct{})
}

func (f *Follower) Run(ctx context.Context) error {
	var ls listenState
	defer f.dropListener(ctx, &ls)
	recheckAt := time.Now().Add(recheckInterval)
	for {
		f.connect(ctx, &ls)
		notice, err := f.await(ctx, ls.listener, recheckAt)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		switch {
		case err == nil:
			ls.failures = 0
			f.handle(ctx, notice)
			continue
		case errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, store.ErrListenerBroken):
			if ls.listener != nil {
				ls.failures = 0
			}
		default:
			f.dropListener(ctx, &ls)
			ls.reconnectAt = time.Now().Add(retryDelay(ls.failures))
			ls.failures++
		}
		f.directoryPass(ctx)
		f.revocationPass(ctx)
		recheckAt = time.Now().Add(recheckInterval)
	}
}

func (f *Follower) connect(ctx context.Context, ls *listenState) {
	if ls.listener != nil || time.Now().Before(ls.reconnectAt) {
		return
	}
	l, err := f.Listen(ctx)
	if err != nil {
		ls.reconnectAt = time.Now().Add(retryDelay(ls.failures))
		ls.failures++
		return
	}
	ls.listener = l
	f.Stats.listenUp.Store(true)
}

func (f *Follower) dropListener(ctx context.Context, ls *listenState) {
	if ls.listener == nil {
		return
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), listenerCloseTimeout)
	defer cancel()
	_ = ls.listener.Close(closeCtx)
	ls.listener = nil
	f.Stats.listenUp.Store(false)
}

func (f *Follower) await(ctx context.Context, l store.Listener, deadline time.Time) (store.Notice, error) {
	waitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if l == nil {
		<-waitCtx.Done()
		return store.NoticeNone, waitCtx.Err()
	}
	return l.Wait(waitCtx)
}

func (f *Follower) handle(ctx context.Context, n store.Notice) {
	switch n {
	case store.NoticeDirectory:
		f.directoryPass(ctx)
	case store.NoticeRevocations:
		f.revocationPass(ctx)
	case store.NoticeNone, store.NoticeOther:
	}
}

func (f *Follower) directoryPass(ctx context.Context) {
	pass, err := drainFeed(ctx, f.Reader, f.Cache, f.Cache.Position())
	if pass.read > 0 {
		f.broadcast()
	}
	if err != nil {
		f.Stats.readErrors.Add(1)
		return
	}
	now := time.Now()
	f.Stats.caughtUpAt.Store(now.UnixNano())
	f.Stats.markStalled(now, pass.pending)
}

func (f *Follower) revocationPass(ctx context.Context) {
	if err := refreshRevocations(ctx, f.Revocations, f.List); err != nil {
		f.Stats.revocationErrors.Add(1)
	}
}
