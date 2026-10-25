package main

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wstest"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

const wsNotSupported = "request not supported"

type wsPeer struct {
	name    string
	user    string
	ws      *wstest.Conn
	gw      *gateway
	feed    *e2e.WSFeed
	fed     chan struct{}
	base    context.Context
	timeout time.Duration
	wait    time.Duration
}

func dialPeer(base context.Context, url, gatewayURL, token string, p *wsPeer) error {
	p.gw = newGateway(base, gatewayURL, token, p.timeout)
	ws, err := dialWS(base, url, token, p.timeout)
	if err != nil {
		return fmt.Errorf("dial %s as %s: %w", url, p.name, err)
	}
	feed, fed := e2e.NewWSFeed(), make(chan struct{})
	go func() {
		defer close(fed)
		feed.Run(ws.Frames(), ws.LoadRooms)
	}()
	p.ws, p.feed, p.fed = ws, feed, fed
	_, err = p.await(0, e2e.WSMatch{Frame: e2e.FrameReady})
	return err
}

func dialWS(base context.Context, url, token string, timeout time.Duration) (*wstest.Conn, error) {
	ctx, cancel := context.WithTimeout(base, timeout)
	defer cancel()
	ws, err := wstest.Dial(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := ws.Auth(token); err != nil {
		_ = ws.Close()
		return nil, err
	}
	return ws, nil
}

func (p *wsPeer) call(req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, error) {
	return callWS(p.base, p.ws, p.timeout, req)
}

func (p *wsPeer) barrier() error {
	resp, err := callWS(p.base, p.ws, p.timeout, historyOverWS())
	if status.Code(err) != codes.Unimplemented || resp.GetMessage() != wsNotSupported {
		return fmt.Errorf("%s: GetHistory over the websocket = %w, want Unimplemented %q", p.name, err, wsNotSupported)
	}
	return nil
}

func historyOverWS() *chatimv1.ClientRequest {
	return &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_GetHistory{GetHistory: &chatimv1.GetHistoryRequest{RoomId: "1"}}}
}

func (p *wsPeer) await(from int, m e2e.WSMatch) (int, error) {
	return p.awaitUntil(from, m, time.Now().Add(p.wait))
}

func (p *wsPeer) awaitUntil(from int, m e2e.WSMatch, deadline time.Time) (int, error) {
	ctx, cancel := context.WithDeadline(p.base, deadline)
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
		<-p.fed
	}
}

func markReadRequest(room string, seq uint64) *chatimv1.ClientRequest {
	return &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_MarkRead{MarkRead: &chatimv1.MarkReadRequest{RoomId: room, Seq: seq}}}
}

func callWS(base context.Context, ws *wstest.Conn, timeout time.Duration, req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, error) {
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
