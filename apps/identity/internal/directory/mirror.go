package directory

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/backoff"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

const (
	backoffBase = time.Second
	backoffCap  = 30 * time.Second
)

type RevocationSource interface {
	Revocations(ctx context.Context) (revocation.Snapshot, error)
}

type Mirror struct {
	Source   Source
	Cache    *Cache
	List     *revocation.List
	Stats    *Stats
	Slots    int
	Recheck  time.Duration
	OnLoaded func()
	OnTrust  func()
}

type mirrorRun struct {
	loadFailures   int
	streamFailures int
	reloadStreak   int
	recheckAt      time.Time
}

func (m *Mirror) Run(ctx context.Context) error {
	if m.Source == nil || m.Cache == nil || m.List == nil || m.Stats == nil || m.Slots <= 0 || m.Recheck <= 0 {
		return errors.New("directory: the mirror needs a source, cache, list, stats, slots above zero and a recheck above zero")
	}
	run := &mirrorRun{recheckAt: time.Now().Add(m.Recheck)}
	for ctx.Err() == nil {
		if len(m.Cache.Token()) == 0 {
			m.load(ctx, run)
			continue
		}
		m.follow(ctx, run)
	}
	return nil
}

func (m *Mirror) follow(ctx context.Context, run *mirrorRun) {
	stream, err := m.Source.OpenStream(ctx, m.Cache.Token())
	if err != nil {
		m.streamFailed(ctx, run, err)
		return
	}
	m.Stats.streamUp.Store(true)
	for {
		ev, has, err := stream.Next(ctx)
		if err != nil || ctx.Err() != nil {
			m.closeStream(ctx, stream)
			m.streamFailed(ctx, run, err)
			return
		}
		run.streamFailures = 0
		m.apply(ctx, run, ev, has)
		if !time.Now().Before(run.recheckAt) {
			m.reloadRevocations(ctx, run)
		}
	}
}

func (m *Mirror) closeStream(ctx context.Context, stream store.ChangeStream) {
	_ = stream.Close(context.WithoutCancel(ctx))
	m.Stats.streamUp.Store(false)
}

func (m *Mirror) streamFailed(ctx context.Context, run *mirrorRun, err error) {
	switch {
	case ctx.Err() != nil:
	case errors.Is(err, store.ErrStreamLost):
		m.Stats.streamReloads.Add(1)
		m.Cache.SetToken(nil)
		wait := retryDelay(run.reloadStreak)
		run.reloadStreak++
		m.pause(ctx, run, wait)
	default:
		m.Stats.readErrors.Add(1)
		wait := retryDelay(run.streamFailures)
		run.streamFailures++
		m.pause(ctx, run, wait)
	}
}

func (m *Mirror) apply(ctx context.Context, run *mirrorRun, ev store.StreamEvent, has bool) {
	if has {
		run.reloadStreak = 0
		for _, kind := range ev.Kinds {
			switch kind {
			case store.EventUser:
				m.Cache.ApplyEntry(ev.User)
			case store.EventTrust:
				if m.OnTrust != nil {
					m.OnTrust()
				}
			case store.EventRevocation:
				m.reloadRevocations(ctx, run)
			}
		}
	}
	if len(ev.Token) > 0 {
		m.Cache.SetToken(ev.Token)
	}
	if !has || ev.CaughtUp {
		m.Stats.caughtUpAt.Store(time.Now().UnixNano())
	}
}

func (m *Mirror) reloadRevocations(ctx context.Context, run *mirrorRun) {
	if err := refreshRevocations(ctx, m.Source, m.List); err != nil && ctx.Err() == nil {
		m.Stats.revocationErrors.Add(1)
	}
	run.recheckAt = time.Now().Add(m.Recheck)
}

func (m *Mirror) pause(ctx context.Context, run *mirrorRun, d time.Duration) {
	until := time.Now().Add(d)
	for {
		now := time.Now()
		if !now.Before(until) {
			return
		}
		if !now.Before(run.recheckAt) {
			m.reloadRevocations(ctx, run)
			continue
		}
		wake := until
		if run.recheckAt.Before(wake) {
			wake = run.recheckAt
		}
		if !backoff.Pause(ctx, wake.Sub(now)) {
			return
		}
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
