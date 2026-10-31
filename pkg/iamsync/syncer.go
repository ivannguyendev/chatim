package iamsync

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

const maxSeenKeys = 64

type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

type KeySink interface {
	SetDynamic(jwks []byte) (int, error)
}

type Shared interface {
	Get(ctx context.Context) ([]byte, error)
	Put(ctx context.Context, value []byte, ttl time.Duration) error
}

type Syncer struct {
	cfg      Config
	list     *revocation.List
	keys     KeySink
	shared   Shared
	doer     Doer
	log      *slog.Logger
	stats    stats
	started  time.Time
	booted   chan struct{}
	bootOnce sync.Once
	keysWake chan struct{}

	mu            sync.Mutex
	onAdded       func(revocation.Added)
	etag          string
	body          []byte
	sharedHash    [sha256.Size]byte
	listReady     bool
	keysReady     bool
	listRefused   int
	keysRefused   int
	timedOut      bool
	lastList      time.Time
	lastKeys      time.Time
	keysStartedOK time.Time
	lastKeyAsk    time.Time
	seen          map[string]time.Time
}

func New(cfg Config, list *revocation.List, keys KeySink, shared Shared, doer Doer, log *slog.Logger) (*Syncer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if list == nil || keys == nil {
		return nil, errors.New("iamsync: list and key sink are required")
	}
	if doer == nil {
		doer = &http.Client{Timeout: cfg.HTTPTimeout, CheckRedirect: refuseRedirect}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Syncer{
		cfg: cfg, list: list, keys: keys, shared: shared, doer: doer, log: log,
		started: time.Now(), booted: make(chan struct{}), keysWake: make(chan struct{}, 1), seen: map[string]time.Time{},
	}
	return s, nil
}

func (s *Syncer) OnAdded(fn func(revocation.Added)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAdded = fn
}

func (s *Syncer) Booted() <-chan struct{} { return s.booted }

func (s *Syncer) Check(c jwtverify.Claims) revocation.Verdict { return s.list.Check(c) }

func (s *Syncer) Run(ctx context.Context) {
	boot := time.AfterFunc(s.cfg.ReadyTimeout, func() { s.bootTimedOut(ctx) })
	defer boot.Stop()
	listTicker := time.NewTicker(s.cfg.RevocationsEvery)
	defer listTicker.Stop()
	keysTicker := time.NewTicker(s.cfg.KeysEvery)
	defer keysTicker.Stop()
	listTick, keysTick := listTicker.C, keysTicker.C
	first, cancelFirst := context.WithTimeout(ctx, s.cfg.ReadyTimeout)
	s.syncList(first)
	s.syncKeys(first)
	cancelFirst()
	s.bootIfReady()
	for {
		select {
		case <-ctx.Done():
			return
		case <-listTick:
			s.syncList(ctx)
			s.bootIfReady()
		case <-keysTick:
			s.syncKeys(ctx)
			s.bootIfReady()
		case <-s.keysWake:
			s.syncKeys(ctx)
			s.bootIfReady()
		}
	}
}

func (s *Syncer) bootTimedOut(ctx context.Context) {
	s.mu.Lock()
	s.timedOut = true
	listRefused, keysRefused := s.listRefused, s.keysRefused
	s.mu.Unlock()
	if listRefused != 0 || keysRefused != 0 {
		s.log.ErrorContext(ctx, "iam refused this node",
			"revocations_status", listRefused, "keys_status", keysRefused, "action", "not ready until iam accepts it; check the node secret and the iam urls")
		return
	}
	s.stats.bootTimeouts.Add(1)
	s.log.WarnContext(ctx, "iam sync not complete before the ready timeout; serving with what is loaded")
	s.bootIfReady()
}

func (s *Syncer) bootIfReady() {
	s.mu.Lock()
	ready := s.listRefused == 0 && s.keysRefused == 0 && (s.timedOut || (s.listReady && s.keysReady))
	s.mu.Unlock()
	if ready {
		s.bootOnce.Do(func() { close(s.booted) })
	}
}

func refusal(status int) int {
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError && status != http.StatusTooManyRequests {
		return status
	}
	return 0
}

func refuseRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (s *Syncer) fetch(ctx context.Context, url string, header http.Header) (int, http.Header, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.HTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header = header
	resp, err := s.doer.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, s.cfg.BodyMax+1))
	if err != nil {
		return 0, nil, nil, err
	}
	if int64(len(body)) > s.cfg.BodyMax {
		return 0, nil, nil, fmt.Errorf("iamsync: response from %s over %d bytes", req.URL.Path, s.cfg.BodyMax)
	}
	return resp.StatusCode, resp.Header, body, nil
}
