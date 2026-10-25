package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

func e2eWSEphemeralKill(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e ws-ephemeral-kill", flag.ContinueOnError)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	url := fs.String("url", "ws://chatim-websocket-1:8080/ws", "websocket endpoint of node 1, where the watcher stays")
	url2 := fs.String("url2", "ws://chatim-websocket-2:8080/ws", "websocket endpoint of node 2, the node to be killed")
	gatewayURL := fs.String("gateway", defaultGatewayURL, "HTTPS gateway endpoint")
	keyFile := fs.String("key", "/keys/"+devtoken.PrivateFile, "private JWK written by dev-keys")
	callTimeout := fs.Duration("call-timeout", 30*time.Second, "timeout of one request")
	wait := fs.Duration("wait", 30*time.Second, "how long to wait for E to show online")
	offline := fs.Duration("offline-within", 60*time.Second, "how long after watching E must show offline")
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
	r := &wsEphemeralRun{base: ctx, key: key, gatewayURL: strings.TrimRight(*gatewayURL, "/"), tenant: st.Tenant, timeout: *callTimeout, wait: *wait}
	b, e := r.peer("B", "e2e-ephkb-"+st.Room), r.peer("E", "e2e-ephke-"+st.Room)
	defer e.close()
	defer b.close()
	if err := r.dial(*url2, e); err != nil {
		return err
	}
	if err := r.dial(*url, b); err != nil {
		return err
	}
	if err := e2e.WriteTokens(filepath.Join(*dir, "ws-ephemeral-kill-tokens"), r.tokens...); err != nil {
		return err
	}
	mark := b.feed.Len()
	if err := b.ws.WatchPresence([]string{e.user}); err != nil {
		return fmt.Errorf("peer B watches presence: %w", err)
	}
	if _, err := b.await(mark, e2e.PresenceMatch(e.user, true)); err != nil {
		return err
	}
	watching := time.Now()
	mark = b.feed.Len()
	fmt.Fprintf(os.Stdout, "watching %s on node 2 online from %s on node 1\n", e.user, b.user)
	if _, err := b.awaitUntil(mark, e2e.PresenceMatch(e.user, false), watching.Add(*offline)); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "offline %s after watching: %s seen offline by %s once its node died\n", time.Since(watching).Round(time.Millisecond), e.user, b.user)
	return nil
}
