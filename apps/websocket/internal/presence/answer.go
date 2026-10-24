package presence

import (
	"context"
	"errors"
	"sort"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/pkg/metrics"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errNoReader    = errors.New("presence: online reader is required")
	errBadCache    = errors.New("presence: cache time must be positive")
	readBounds     = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}
	errResultCount = errors.New("presence: reader returned a different number of results")
)

type Reader interface {
	Online(ctx context.Context, keys []string) ([]bool, error)
}

type Answerer struct {
	reader   Reader
	cache    *onlineCache
	reads    atomic.Uint64
	failures atomic.Uint64
	latency  *histogram
}

func NewAnswerer(fresh time.Duration, reader Reader) (*Answerer, error) {
	switch {
	case reader == nil:
		return nil, errNoReader
	case fresh <= 0:
		return nil, errBadCache
	}
	return &Answerer{reader: reader, cache: newOnlineCache(fresh), latency: newHistogram(readBounds)}, nil
}

func (a *Answerer) Answer(ctx context.Context, tenant string, users []string) ([]*chatimv1.PresenceEntry, error) {
	now := time.Now()
	entries := make([]*chatimv1.PresenceEntry, len(users))
	var missing []int
	for i, id := range users {
		online := a.cache.online(user{tenant: tenant, id: id}, now)
		entries[i] = &chatimv1.PresenceEntry{UserId: id, Online: online}
		if !online {
			missing = append(missing, i)
		}
	}
	if len(missing) == 0 {
		return entries, nil
	}
	online, err := a.read(ctx, tenant, users, missing)
	if err != nil {
		return nil, err
	}
	for j, i := range missing {
		u := user{tenant: tenant, id: users[i]}
		if online[j] {
			entries[i].Online = true
			a.cache.put(u, now)
		} else {
			a.cache.forget(u)
		}
	}
	return entries, nil
}

func (a *Answerer) read(ctx context.Context, tenant string, users []string, missing []int) ([]bool, error) {
	keys := make([]string, len(missing))
	for j, i := range missing {
		keys[j] = Key(tenant, users[i])
	}
	a.reads.Add(1)
	began := time.Now()
	online, err := a.reader.Online(ctx, keys)
	a.latency.observe(time.Since(began))
	if err == nil && len(online) != len(keys) {
		err = errResultCount
	}
	if err != nil {
		if ctx.Err() == nil {
			a.failures.Add(1)
		}
		return nil, err
	}
	return online, nil
}

func (a *Answerer) Prune(now time.Time) { a.cache.prune(now) }

func (a *Answerer) Sources() []metrics.Source {
	return []metrics.Source{
		{Name: "presence_reads_total", Help: "Presence reads sent to the presence Redis, one MGET each.", Read: counterOf(&a.reads)},
		{Name: "presence_read_failures_total", Help: "Presence reads that failed or timed out; their answer was not sent.", Read: counterOf(&a.failures)},
		{Name: "presence_cached_users", Help: "Users kept online in the presence read cache.", Gauge: true, Read: func() float64 { return float64(a.cache.size.Load()) }},
		{Name: "presence_read_seconds", Help: "Presence Redis read latency, in seconds.", Histogram: a.latency.read},
	}
}

type histogram struct {
	bounds  []float64
	buckets []atomic.Uint64
	count   atomic.Uint64
	nanos   atomic.Int64
}

func newHistogram(bounds []float64) *histogram {
	return &histogram{bounds: bounds, buckets: make([]atomic.Uint64, len(bounds))}
}

func (h *histogram) observe(elapsed time.Duration) {
	elapsed = max(elapsed, 0)
	if i := sort.SearchFloat64s(h.bounds, elapsed.Seconds()); i < len(h.bounds) {
		h.buckets[i].Add(1)
	}
	h.nanos.Add(int64(elapsed))
	h.count.Add(1)
}

func (h *histogram) read() metrics.Histogram {
	out := metrics.Histogram{Bounds: h.bounds, Cumulative: make([]uint64, len(h.bounds))}
	var running uint64
	for i := range h.bounds {
		running += h.buckets[i].Load()
		out.Cumulative[i] = running
	}
	out.Count = max(h.count.Load(), running)
	out.Sum = time.Duration(h.nanos.Load()).Seconds()
	return out
}
