package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/wstest"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

func e2eWSNodeStop(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e ws-node-stop", flag.ContinueOnError)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	url := fs.String("url", "ws://chatim-websocket-1:8080/ws", "websocket endpoint of node 1, the node to be stopped")
	url2 := fs.String("url2", "ws://chatim-websocket-2:8080/ws", "websocket endpoint of node 2, where A reconnects")
	gatewayURL := fs.String("gateway", defaultGatewayURL, "HTTPS gateway endpoint for room management")
	keyFile := fs.String("key", "/keys/"+devtoken.PrivateFile, "private JWK written by dev-keys")
	callTimeout := fs.Duration("call-timeout", 30*time.Second, "timeout of one request")
	wait := fs.Duration("wait", 30*time.Second, "how long to wait for one expected frame")
	stopWithin := fs.Duration("stop-within", 90*time.Second, "how long after the on websocket-1 line node 1 must end A's connection")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	key, err := devtoken.LoadPrivateFile(*keyFile)
	if err != nil {
		return err
	}
	r := &wsResilienceRun{base: ctx, key: key, gatewayURL: strings.TrimRight(*gatewayURL, "/"), tenant: st.Tenant, timeout: *callTimeout, wait: *wait}
	a, b := r.peer("A", "e2e-wsna-"+st.Room), r.peer("B", "e2e-wsnb-"+st.Room)
	defer b.close()
	defer a.close()
	tokenA, _, err := r.sign(a.user, devTokenTTL)
	if err != nil {
		return err
	}
	tokenB, _, err := r.sign(b.user, devTokenTTL)
	if err != nil {
		return err
	}
	if err := e2e.WriteTokens(filepath.Join(*dir, "ws-node-stop-tokens"), r.tokens...); err != nil {
		return err
	}
	if err := dialPeer(ctx, *url, r.gatewayURL, tokenA, a); err != nil {
		return err
	}
	if err := dialPeer(ctx, *url2, r.gatewayURL, tokenB, b); err != nil {
		return err
	}
	room, err := groupOf(b, "e2e websocket node stop", a, b)
	if err != nil {
		return err
	}
	if err := openAndBarrier(a, room); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "on websocket-1: %s viewing group %s, %s on websocket-2\n", a.user, room, b.user)
	ended, err := connectionEnd(a, *stopWithin)
	if err != nil {
		return err
	}
	a.close()
	if err := dialPeer(ctx, *url2, r.gatewayURL, tokenA, a); err != nil {
		return err
	}
	if err := openAndBarrier(a, room); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "reconnected on websocket-2: %s after node 1 ended its connection (%s)\n", a.user, ended)
	mark := a.feed.Len()
	if err := sendText(b, room, 1, "node stop 1"); err != nil {
		return err
	}
	if _, err := a.await(mark, e2e.CreatedMatch(room, 1)); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "heard on websocket-2: %s received seq 1 of %s from %s\n", a.user, room, b.user)
	return nil
}

func connectionEnd(p *wsPeer, within time.Duration) (string, error) {
	start := time.Now()
	select {
	case <-p.ws.Done():
	case <-time.After(within):
		return "", fmt.Errorf("%s: the connection to node 1 stayed open %v after the node was to stop", p.name, within)
	case <-p.base.Done():
		return "", p.base.Err()
	}
	waited := time.Since(start).Round(100 * time.Millisecond)
	var code uint16
	closeErr, closed := errors.AsType[*wstest.CloseError](p.ws.Err())
	if closed {
		code = closeErr.Code
	}
	if err := e2e.CheckGoingAway(p.name+"'s connection to the stopping node 1", code, closed); err != nil {
		return "", fmt.Errorf("%w (ended with %w after %v)", err, p.ws.Err(), waited)
	}
	return fmt.Sprintf("close %d after %v", code, waited), nil
}
