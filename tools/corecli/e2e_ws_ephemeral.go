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

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

const (
	typingWithin   = time.Second
	typingQuiet    = 2 * time.Second
	offlineWithin  = 35 * time.Second
	stayOnlineFor  = 20 * time.Second
	reconnectAfter = 3 * time.Second
)

type wsEphemeralRun struct {
	base          context.Context
	key           devtoken.KeyPair
	url, url2     string
	gatewayURL    string
	tenant        string
	timeout, wait time.Duration
	a, a2, b, c   *wsPeer
	d             *wsPeer
	room          string
	dClosedAt     time.Time
	tokens        []string
	notes         []string
}

func e2eWSEphemeral(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e ws-ephemeral", flag.ContinueOnError)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	url := fs.String("url", "ws://chatim-websocket-1:8080/ws", "websocket endpoint of node 1")
	url2 := fs.String("url2", "ws://chatim-websocket-2:8080/ws", "websocket endpoint of node 2")
	gatewayURL := fs.String("gateway", defaultGatewayURL, "HTTPS gateway endpoint for room management")
	keyFile := fs.String("key", "/keys/"+devtoken.PrivateFile, "private JWK written by dev-keys")
	callTimeout := fs.Duration("call-timeout", 30*time.Second, "timeout of one request")
	wait := fs.Duration("wait", 30*time.Second, "how long to wait for one expected frame")
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
	r := &wsEphemeralRun{base: ctx, key: key, url: *url, url2: *url2, gatewayURL: strings.TrimRight(*gatewayURL, "/"), tenant: st.Tenant, timeout: *callTimeout, wait: *wait}
	aUser := "e2e-epha-" + st.Room
	r.a = r.peer("A", aUser)
	r.a2 = r.peer("A2", aUser)
	r.b = r.peer("B", "e2e-ephb-"+st.Room)
	r.c = r.peer("C", "e2e-ephc-"+st.Room)
	r.d = r.peer("D", "e2e-ephd-"+st.Room)
	if err := r.run(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "websocket ephemeral ok on group %s: %s\n", r.room, strings.Join(r.notes, "; "))
	return e2e.WriteTokens(filepath.Join(*dir, "ws-ephemeral-tokens"), r.tokens...)
}

func (r *wsEphemeralRun) peer(name, user string) *wsPeer {
	return &wsPeer{name: name, user: user, base: r.base, timeout: r.timeout, wait: r.wait}
}

func (r *wsEphemeralRun) run() error {
	steps := []wsEventsStep{
		{"A on node 1, B, C and A's second device on node 2 join a group; B and A2 open it", r.connect},
		{"A types: B sees it within 1s, C and A2 see nothing, A stops and B sees the end", r.typingSeen},
		{"D, outside the group, types into it on node 1 and B sees nothing", r.outsiderDropped},
		{"B watches A and D: A online, D offline within 35s of closing", r.presenceSeen},
		{"A2 closes, A reconnects to node 1 after 3s and B never sees A offline for 20s", r.reconnectStaysOnline},
	}
	defer func() {
		for _, p := range []*wsPeer{r.a, r.a2, r.b, r.c, r.d} {
			p.close()
		}
	}()
	for i, step := range steps {
		if err := step.run(); err != nil {
			return fmt.Errorf("websocket ephemeral step %d (%s): %w", i+1, step.name, err)
		}
		fmt.Fprintf(os.Stderr, "websocket ephemeral step %d ok: %s\n", i+1, step.name)
	}
	return nil
}

func (r *wsEphemeralRun) dial(url string, p *wsPeer) error {
	token, err := devSign(r.key, r.tenant, p.user, time.Now(), devTokenTTL)
	if err != nil {
		return err
	}
	r.tokens = append(r.tokens, token)
	return dialPeer(r.base, url, r.gatewayURL, token, p)
}

func (r *wsEphemeralRun) connect() error {
	for _, d := range []struct {
		url string
		p   *wsPeer
	}{{r.url, r.a}, {r.url2, r.b}, {r.url2, r.c}, {r.url2, r.a2}} {
		if err := r.dial(d.url, d.p); err != nil {
			return err
		}
	}
	resp, err := viaGateway(r.a.gw, r.a.gw.core.CreateRoom, &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "e2e websocket ephemeral", Members: []string{r.a.user, r.b.user, r.c.user}, RequestId: randomCID(),
	})
	if err != nil {
		return fmt.Errorf("create room: %w", err)
	}
	if r.room = resp.GetRoom().GetId(); r.room == "" {
		return errors.New("create room returned no room id")
	}
	for _, p := range []*wsPeer{r.a, r.b, r.c, r.a2} {
		if _, err := p.await(0, e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "member_added", Room: r.room, User: p.user}); err != nil {
			return err
		}
	}
	for _, p := range []*wsPeer{r.b, r.a2} {
		if err := p.ws.OpenRoom(r.room); err != nil {
			return fmt.Errorf("%s opens %s: %w", p.name, r.room, err)
		}
		if err := p.barrier(); err != nil {
			return err
		}
	}
	return nil
}

func (r *wsEphemeralRun) typingSeen() error {
	markB, markC, markA2 := r.b.feed.Len(), r.c.feed.Len(), r.a2.feed.Len()
	start := time.Now()
	if err := r.a.ws.Typing(r.room, false); err != nil {
		return fmt.Errorf("peer A typing: %w", err)
	}
	if _, err := r.b.awaitUntil(markB, e2e.TypingMatch(r.room, r.a.user, true), start.Add(typingWithin)); err != nil {
		return err
	}
	latency := time.Since(start)
	if !r.pauseUntil(start.Add(typingQuiet)) {
		return r.base.Err()
	}
	for _, q := range []struct {
		p    *wsPeer
		mark int
	}{{r.c, markC}, {r.a2, markA2}} {
		if err := q.p.absent(q.mark, e2e.WSMatch{Frame: e2e.FrameTyping}); err != nil {
			return err
		}
	}
	stop := time.Now()
	if err := r.a.ws.Typing(r.room, true); err != nil {
		return fmt.Errorf("peer A stops typing: %w", err)
	}
	if _, err := r.b.awaitUntil(markB, e2e.TypingMatch(r.room, r.a.user, false), stop.Add(typingWithin)); err != nil {
		return err
	}
	r.notes = append(r.notes, fmt.Sprintf("typing A on node 1 to B on node 2 in %s, its end in %s", latency.Round(time.Millisecond), time.Since(stop).Round(time.Millisecond)))
	return nil
}

func (r *wsEphemeralRun) pauseUntil(at time.Time) bool {
	wait := time.Until(at)
	if wait <= 0 {
		return r.base.Err() == nil
	}
	return backoff.Pause(r.base, wait)
}
