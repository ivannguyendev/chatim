package main

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wsclient"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

type wsPeer struct {
	name    string
	user    string
	ws      *wsclient.Client
	feed    *e2e.WSFeed
	applier *wsclient.Applier
	base    context.Context
	timeout time.Duration
	wait    time.Duration
}

func dialPeer(base context.Context, url, token string, p *wsPeer) error {
	p.feed, p.applier = e2e.NewWSFeed(), wsclient.NewApplier(wsclient.DefaultRecentIDs)
	ctx, cancel := context.WithTimeout(base, p.timeout)
	defer cancel()
	ws, err := wsclient.Dial(ctx, url, token,
		wsclient.WithApplier(p.applier),
		wsclient.OnEvent(p.feed.Event),
		wsclient.OnNotification(p.feed.Notification),
		wsclient.OnReady(p.feed.Ready),
		wsclient.OnResync(p.feed.Resync),
		wsclient.OnRoomClosed(p.feed.RoomClosed),
		wsclient.OnGap(p.feed.Gap),
	)
	if err != nil {
		return fmt.Errorf("dial %s as %s: %w", url, p.name, err)
	}
	p.ws = ws
	_, err = p.await(0, e2e.WSMatch{Frame: e2e.FrameReady})
	return err
}

func (p *wsPeer) call(req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, error) {
	return callWS(p.base, p.ws, p.timeout, req)
}

func (p *wsPeer) await(from int, m e2e.WSMatch) (int, error) {
	ctx, cancel := context.WithTimeout(p.base, p.wait)
	defer cancel()
	i, err := p.feed.Wait(ctx, from, m)
	if err != nil {
		return i, fmt.Errorf("%s: %w", p.name, err)
	}
	return i, nil
}

func (p *wsPeer) absent(from int, m e2e.WSMatch) error {
	if _, e, ok := p.feed.Find(from, m); ok {
		return fmt.Errorf("%s received %+v, want nothing matching %v", p.name, e, m)
	}
	return nil
}

func (p *wsPeer) close() {
	if p.ws != nil {
		_ = p.ws.Close()
	}
}

func callWS(base context.Context, ws *wsclient.Client, timeout time.Duration, req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, error) {
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(base, timeout)
		resp, err := ws.Call(ctx, req)
		cancel()
		if status.Code(err) != codes.Unavailable || attempt == wsCallAttempts {
			return resp, err
		}
		if !backoff.Pause(base, wsRetryPause) {
			return nil, base.Err()
		}
	}
}

func devSign(key devtoken.KeyPair, tenant, user string, issued time.Time, ttl time.Duration) (string, error) {
	return key.Sign(devtoken.Claims{Issuer: devIssuer, Audience: devAudience, Subject: user, Tenant: tenant, IssuedAt: issued, TTL: ttl})
}
