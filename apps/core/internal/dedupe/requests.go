package dedupe

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/lru"
)

const RequestCacheSize = 4096

type RequestStatus int

const (
	RequestNew RequestStatus = iota
	RequestDone
	RequestBusy
)

func (s RequestStatus) String() string {
	switch s {
	case RequestDone:
		return "done"
	case RequestBusy:
		return "busy"
	default:
		return "new"
	}
}

func RequestKey(room uint64, user, requestID string) Key {
	return Key{Room: room, User: user, CID: requestID, Space: SpaceRequest}
}

type Requests struct {
	reg  Registry
	ttl  time.Duration
	mu   sync.Mutex
	done *lru.Cache[Key, time.Time]
}

func NewRequests(reg Registry, ttl time.Duration) (*Requests, error) {
	switch {
	case reg == nil:
		return nil, fmt.Errorf("%w: request dedupe needs a registry", apperr.ErrInvalidArgument)
	case ttl <= 0:
		return nil, fmt.Errorf("%w: request dedupe ttl %v must be positive", apperr.ErrInvalidArgument, ttl)
	}
	return &Requests{reg: reg, ttl: ttl, done: lru.New[Key, time.Time](RequestCacheSize)}, nil
}

func (r *Requests) Begin(ctx context.Context, k Key) (RequestStatus, error) {
	if err := ctx.Err(); err != nil {
		return RequestNew, err
	}
	if r.remembered(k) {
		return RequestDone, nil
	}
	verdicts, err := r.reg.Reserve(ctx, []Key{k})
	if cerr := ctx.Err(); cerr != nil {
		return RequestNew, cerr
	}
	switch v := onlyVerdict(verdicts, err); v.Status {
	case Committed:
		r.remember(k, v.Record.CreatedAt)
		return RequestDone, nil
	case PendingHere, PendingElsewhere:
		return RequestBusy, nil
	default:
		return RequestNew, nil
	}
}

func (r *Requests) Finish(ctx context.Context, k Key, rec Record) {
	r.remember(k, rec.CreatedAt)
	_ = r.reg.Commit(ctx, []Entry{{Key: k, Record: rec}})
}

func (r *Requests) Cancel(ctx context.Context, k Key) {
	_ = r.reg.Abort(ctx, []Key{k})
}

func onlyVerdict(verdicts []Verdict, err error) Verdict {
	if err != nil || len(verdicts) != 1 {
		return Verdict{Status: Absent}
	}
	return verdicts[0]
}

func (r *Requests) remembered(k Key) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.done.Get(k)
	if !ok {
		return false
	}
	if time.Since(at) >= r.ttl {
		r.done.Remove(k)
		return false
	}
	return true
}

func (r *Requests) remember(k Key, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done.Put(k, at)
}
