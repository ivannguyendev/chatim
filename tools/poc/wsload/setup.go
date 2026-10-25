package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/sync/errgroup"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/v1/chatimv1connect"
	"github.com/ivannguyendev/chatim/pkg/wstest"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
	"github.com/ivannguyendev/chatim/tools/poc/internal/openloop"
	"github.com/ivannguyendev/chatim/tools/poc/internal/wsload"
)

const (
	devIssuer      = "chatim-dev"
	devAudience    = "chatim-websocket"
	setupWorkers   = 16
	setupTimeout   = 30 * time.Second
	setupAttempts  = 5
	setupRetryWait = 500 * time.Millisecond
	tokenTTLBase   = time.Hour
)

type load struct {
	cfg     config
	run     string
	layout  wsload.Layout
	clients []*client
	rooms   []string
	report  report

	delivery wsload.Histogram
	connect  wsload.Histogram
	sends    openloop.Stats
	errs     errorCounts

	reconnect  bool
	ready      atomic.Int64
	failed     atomic.Int64
	lastReady  atomic.Int64
	retryLater atomic.Int64
	loadFailed atomic.Int64
	resyncs    atomic.Int64
	skipped    atomic.Int64
	delivered  atomic.Int64
	duplicates atomic.Int64
	expected   atomic.Int64
	drops      dropClock
}

func newLoad(c config, key devtoken.KeyPair, run string) (*load, error) {
	l := &load{cfg: c, run: run, layout: c.layout(), rooms: make([]string, c.groups), reconnect: c.scenario == scenarioKill}
	ttl := tokenTTLBase + 3*c.duration
	l.clients = make([]*client, c.conns)
	for i := range l.clients {
		user := run + "-u" + strconv.Itoa(i)
		token, err := key.Sign(devtoken.Claims{Issuer: devIssuer, Audience: devAudience, Subject: user, Tenant: c.tenant, TTL: ttl})
		if err != nil {
			return nil, err
		}
		l.clients[i] = &client{l: l, user: user, token: token, node: l.layout.NodeOf(i), first: make(chan struct{})}
	}
	return l, nil
}

func (l *load) createGroups(ctx context.Context) error {
	httpClient := &http.Client{Timeout: setupTimeout, Transport: &http.Transport{MaxIdleConnsPerHost: setupWorkers}}
	core := chatimv1connect.NewCoreServiceClient(httpClient, l.cfg.gateway)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(setupWorkers)
	for group := range l.cfg.groups {
		g.Go(func() error { return l.createGroup(gctx, core, group) })
	}
	if err := g.Wait(); err != nil {
		return err
	}
	for i, c := range l.clients {
		c.room = l.rooms[l.layout.GroupOf(i)]
	}
	return nil
}

func (l *load) createGroup(ctx context.Context, core chatimv1connect.CoreServiceClient, group int) error {
	creator := l.clients[l.layout.SenderOf(group)]
	members := l.layout.Members(group)
	users := make([]string, len(members))
	for i, m := range members {
		users[i] = l.clients[m].user
	}
	msg := &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: l.run + " group " + strconv.Itoa(group),
		Members: users, RequestId: l.run + "-g" + strconv.Itoa(group),
	}
	for attempt := 1; ; attempt++ {
		req := connect.NewRequest(msg)
		req.Header().Set("Authorization", "Bearer "+creator.token)
		resp, err := core.CreateRoom(ctx, req)
		if err == nil {
			if l.rooms[group] = resp.Msg.GetRoom().GetId(); l.rooms[group] == "" {
				return fmt.Errorf("group %d: CreateRoom returned no room id", group)
			}
			return nil
		}
		code := connect.CodeOf(err)
		if (code != connect.CodeUnavailable && code != connect.CodeResourceExhausted) || attempt == setupAttempts {
			return fmt.Errorf("group %d: create room: %w", group, err)
		}
		if !backoff.Pause(ctx, backoff.Jitter(setupRetryWait)) {
			return ctx.Err()
		}
	}
}

type errorCounts struct {
	mu sync.Mutex
	m  map[string]int
}

func (e *errorCounts) add(stage string, err error) {
	key := stage + ": " + errorKind(err)
	e.mu.Lock()
	if e.m == nil {
		e.m = map[string]int{}
	}
	e.m[key]++
	e.mu.Unlock()
}

func (e *errorCounts) snapshot() map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.m)
}

func errorKind(err error) string {
	const maxKind = 100
	if closeErr, ok := errors.AsType[*wstest.CloseError](err); ok {
		return "close " + strconv.Itoa(int(closeErr.Code))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	s := err.Error()
	if len(s) > maxKind {
		s = s[:maxKind]
	}
	return s
}
