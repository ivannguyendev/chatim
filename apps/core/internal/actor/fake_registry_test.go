package actor_test

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
)

type fakeRegistry struct {
	mu        sync.Mutex
	committed map[dedupe.Key]dedupe.Record
	forced    map[dedupe.Key]dedupe.Status
	err       error
	delay     time.Duration
	reserves  [][]dedupe.Key
	commits   [][]dedupe.Entry
	aborts    [][]dedupe.Key
}

func (f *fakeRegistry) Reserve(_ context.Context, keys []dedupe.Key) ([]dedupe.Verdict, error) {
	f.mu.Lock()
	delay := f.delay
	f.mu.Unlock()
	time.Sleep(delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reserves = append(f.reserves, slices.Clone(keys))
	if f.err != nil {
		return nil, f.err
	}
	out := make([]dedupe.Verdict, len(keys))
	for i, k := range keys {
		rec, done := f.committed[k]
		switch status, ok := f.forced[k]; {
		case ok:
			out[i] = dedupe.Verdict{Status: status, Record: rec}
		case done:
			out[i] = dedupe.Verdict{Status: dedupe.Committed, Record: rec}
		default:
			out[i] = dedupe.Verdict{Status: dedupe.Reserved}
		}
	}
	return out, nil
}

func (f *fakeRegistry) Commit(_ context.Context, entries []dedupe.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits = append(f.commits, slices.Clone(entries))
	if f.err != nil {
		return f.err
	}
	if f.committed == nil {
		f.committed = map[dedupe.Key]dedupe.Record{}
	}
	for _, e := range entries {
		f.committed[e.Key] = e.Record
	}
	return nil
}

func (f *fakeRegistry) Abort(_ context.Context, keys []dedupe.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborts = append(f.aborts, slices.Clone(keys))
	return f.err
}

func (f *fakeRegistry) force(k dedupe.Key, status dedupe.Status, rec dedupe.Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.forced == nil {
		f.forced = map[dedupe.Key]dedupe.Status{}
	}
	if f.committed == nil {
		f.committed = map[dedupe.Key]dedupe.Record{}
	}
	f.forced[k], f.committed[k] = status, rec
}

func (f *fakeRegistry) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeRegistry) calls() (reserves [][]dedupe.Key, commits [][]dedupe.Entry, aborts [][]dedupe.Key) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reserves), slices.Clone(f.commits), slices.Clone(f.aborts)
}

func remoteKey(room uint64, user, cid string) dedupe.Key {
	return dedupe.Key{Room: room, User: user, CID: cid}
}
