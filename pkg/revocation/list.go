package revocation

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

type Verdict int

const (
	Allowed Verdict = iota
	SessionRevoked
	TenantRevoked
)

type Session struct {
	ID        string    `json:"session_id"`
	Tenant    string    `json:"tenant_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Tenant struct {
	ID            string    `json:"tenant_id"`
	RevokedBefore time.Time `json:"revoked_before"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type Snapshot struct {
	Sessions []Session `json:"sessions"`
	Tenants  []Tenant  `json:"tenants"`
}

func (s Snapshot) MarshalJSON() ([]byte, error) {
	type plain Snapshot
	out := plain(s)
	if out.Sessions == nil {
		out.Sessions = []Session{}
	}
	if out.Tenants == nil {
		out.Tenants = []Tenant{}
	}
	return json.Marshal(out)
}

type Added struct {
	Sessions []string
	Tenants  []string
}

func (a Added) Empty() bool { return len(a.Sessions) == 0 && len(a.Tenants) == 0 }

type Config struct {
	Leeway   time.Duration
	MaxKeep  time.Duration
	MaxAhead time.Duration
}

type sessionEntry struct {
	tenant string
	until  time.Time
}

type tenantEntry struct {
	before time.Time
	until  time.Time
}

type List struct {
	cfg      Config
	mu       sync.RWMutex
	sessions map[string]sessionEntry
	tenants  map[string]tenantEntry
	dropped  atomic.Uint64
}

func New(cfg Config) (*List, error) {
	switch {
	case cfg.Leeway < 0:
		return nil, errors.New("revocation: leeway must not be negative")
	case cfg.MaxKeep <= 0:
		return nil, errors.New("revocation: max keep must be positive")
	case cfg.MaxAhead < 0:
		return nil, errors.New("revocation: max ahead must not be negative")
	}
	return &List{cfg: cfg, sessions: map[string]sessionEntry{}, tenants: map[string]tenantEntry{}}, nil
}

func (l *List) Merge(s Snapshot, now time.Time) Added {
	var added Added
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range s.Sessions {
		if e.ID == "" || e.Tenant == "" || !l.keepable(e.ExpiresAt, now) {
			l.dropped.Add(1)
			continue
		}
		until := e.ExpiresAt.Add(l.cfg.Leeway)
		old, ok := l.sessions[e.ID]
		if ok && !until.After(old.until) {
			continue
		}
		l.sessions[e.ID] = sessionEntry{tenant: e.Tenant, until: until}
		if !ok {
			added.Sessions = append(added.Sessions, e.ID)
		}
	}
	for _, e := range s.Tenants {
		if e.ID == "" || !l.keepable(e.ExpiresAt, now) || e.RevokedBefore.After(now.Add(l.cfg.MaxAhead)) {
			l.dropped.Add(1)
			continue
		}
		old, ok := l.tenants[e.ID]
		entry := tenantEntry{before: e.RevokedBefore, until: e.ExpiresAt.Add(l.cfg.Leeway)}
		if ok {
			entry.before = later(old.before, entry.before)
			entry.until = later(old.until, entry.until)
		}
		l.tenants[e.ID] = entry
		if !ok || entry.before.After(old.before) {
			added.Tenants = append(added.Tenants, e.ID)
		}
	}
	return added
}

func (l *List) keepable(expires, now time.Time) bool {
	return expires.Add(l.cfg.Leeway).After(now) && !expires.After(now.Add(l.cfg.MaxKeep))
}

func (l *List) Check(c jwtverify.Claims) Verdict {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if t, ok := l.tenants[c.Tenant]; ok && !c.IssuedAt.After(t.before) {
		return TenantRevoked
	}
	if s, ok := l.sessions[c.Session]; ok && s.tenant == c.Tenant {
		return SessionRevoked
	}
	return Allowed
}

func (l *List) Prune(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, e := range l.sessions {
		if !e.until.After(now) {
			delete(l.sessions, id)
		}
	}
	for id, e := range l.tenants {
		if !e.until.After(now) {
			delete(l.tenants, id)
		}
	}
}

func (l *List) Counts() (sessions, tenants int) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.sessions), len(l.tenants)
}

func (l *List) Dropped() uint64 { return l.dropped.Load() }

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
