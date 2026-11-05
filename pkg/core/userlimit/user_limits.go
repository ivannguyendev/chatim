package userlimit

import (
	"container/list"
	"errors"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

var (
	ErrRate     = errors.New("userlimit: user over its request rate")
	ErrInflight = errors.New("userlimit: user over its requests in flight")
)

type Key struct {
	Tenant string
	User   string
}

type UserConfig struct {
	Rate     int
	Burst    int
	Inflight int
	Capacity int
	Now      func() time.Time
}

type entry struct {
	key      Key
	bucket   *rate.Limiter
	inflight int
}

type Users struct {
	cfg     UserConfig
	mu      sync.Mutex
	entries map[Key]*list.Element
	order   *list.List
}

func NewUsers(cfg UserConfig) (*Users, error) {
	if cfg.Rate <= 0 || cfg.Burst <= 0 || cfg.Inflight <= 0 || cfg.Capacity <= 0 {
		return nil, errors.New("userlimit: rate, burst, inflight and capacity must be positive")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Users{cfg: cfg, entries: make(map[Key]*list.Element, min(cfg.Capacity, 1024)), order: list.New()}, nil
}

func (u *Users) Acquire(key Key) (release func(), err error) {
	now := u.cfg.Now()
	u.mu.Lock()
	defer u.mu.Unlock()
	e := u.lookup(key)
	if e.inflight >= u.cfg.Inflight {
		return nil, ErrInflight
	}
	if !e.bucket.AllowN(now, 1) {
		return nil, ErrRate
	}
	e.inflight++
	var once sync.Once
	return func() { once.Do(func() { u.release(e) }) }, nil
}

func (u *Users) Len() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.order.Len()
}

func (u *Users) lookup(key Key) *entry {
	if el, ok := u.entries[key]; ok {
		u.order.MoveToFront(el)
		return el.Value.(*entry)
	}
	u.trim(u.cfg.Capacity - 1)
	e := &entry{key: key, bucket: rate.NewLimiter(rate.Limit(u.cfg.Rate), u.cfg.Burst)}
	u.entries[key] = u.order.PushFront(e)
	return e
}

func (u *Users) trim(size int) {
	for el := u.order.Back(); el != nil && u.order.Len() > size; {
		prev := el.Prev()
		if e := el.Value.(*entry); e.inflight == 0 {
			u.order.Remove(el)
			delete(u.entries, e.key)
		}
		el = prev
	}
}

func (u *Users) release(e *entry) {
	u.mu.Lock()
	defer u.mu.Unlock()
	e.inflight--
	u.trim(u.cfg.Capacity)
}
