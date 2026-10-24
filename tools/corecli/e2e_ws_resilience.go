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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

const (
	waitingQuiet      = 2 * time.Second
	frameErrorSpacing = 1100 * time.Millisecond
)

type wsResilienceRun struct {
	base          context.Context
	key           devtoken.KeyPair
	url           string
	gatewayURL    string
	tenant        string
	timeout, wait time.Duration
	shortTTL      time.Duration
	expiryWithin  time.Duration
	userCap       int
	u, b          *wsPeer
	vs            []*wsPeer
	room          string
	issued        time.Time
	waitingFrom   int
	tokens        []string
	notes         []string
}

func e2eWSResilience(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e ws-resilience", flag.ContinueOnError)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	url := fs.String("url", "ws://chatim-websocket-1:8080/ws", "websocket endpoint of node 1")
	gatewayURL := fs.String("gateway", defaultGatewayURL, "HTTPS gateway endpoint for room management")
	keyFile := fs.String("key", "/keys/"+devtoken.PrivateFile, "private JWK written by dev-keys")
	callTimeout := fs.Duration("call-timeout", 30*time.Second, "timeout of one request")
	wait := fs.Duration("wait", 30*time.Second, "how long to wait for one expected frame")
	shortTTL := fs.Duration("token-ttl", 20*time.Second, "lifetime of the short token of U")
	expiryWithin := fs.Duration("expired-within", 70*time.Second, "how long after signing U's short token auth_required EXPIRED must come (ttl + WS_JWT_LEEWAY + margin)")
	userCap := fs.Int("user-connections", 10, "WS_USER_CONNECTIONS_MAX of the websocket node")
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
	r := &wsResilienceRun{
		base: ctx, key: key, url: *url, gatewayURL: strings.TrimRight(*gatewayURL, "/"), tenant: st.Tenant,
		timeout: *callTimeout, wait: *wait, shortTTL: *shortTTL, expiryWithin: *expiryWithin, userCap: *userCap,
	}
	r.u = r.peer("U", "e2e-wsru-"+st.Room)
	r.b = r.peer("B", "e2e-wsrb-"+st.Room)
	runErr := r.run()
	if len(r.tokens) > 0 {
		if err := e2e.WriteTokens(filepath.Join(*dir, "ws-resilience-tokens"), r.tokens...); err != nil {
			return errors.Join(runErr, err)
		}
	}
	if runErr != nil {
		return runErr
	}
	fmt.Fprintf(os.Stderr, "websocket resilience ok on group %s: %s\n", r.room, strings.Join(r.notes, "; "))
	return nil
}

func (r *wsResilienceRun) peer(name, user string) *wsPeer {
	return &wsPeer{name: name, user: user, base: r.base, timeout: r.timeout, wait: r.wait}
}

func (r *wsResilienceRun) run() error {
	steps := []wsEventsStep{
		{"U logs in with a short token, B creates a group of U and B through the gateway, U opens it and hears B's first message", r.connect},
		{"U's token runs out and U gets auth_required EXPIRED", r.expired},
		{"B sends while U waits: B hears it, U hears nothing and a write of U is refused UNAUTHENTICATED", r.silentWhileWaiting},
		{"U sends a fresh token, gets ready, opens the group again and hears B's next message after a gap of the missed seq", r.relogin},
		{"a text frame gets frame_error UNSUPPORTED and a garbage frame frame_error INVALID, the connection stays open and answers a request", r.frameErrors},
		{"V opens the user cap of connections to node 1, each gets ready, and one more is closed with 4009", r.userCapped},
	}
	defer func() {
		for _, p := range append([]*wsPeer{r.u, r.b}, r.vs...) {
			p.close()
		}
	}()
	for i, step := range steps {
		if err := step.run(); err != nil {
			return fmt.Errorf("websocket resilience step %d (%s): %w", i+1, step.name, err)
		}
		fmt.Fprintf(os.Stderr, "websocket resilience step %d ok: %s\n", i+1, step.name)
	}
	return nil
}

func (r *wsResilienceRun) sign(user string, ttl time.Duration) (string, time.Time, error) {
	issued := time.Now()
	token, err := devSign(r.key, r.tenant, user, issued, ttl)
	if err != nil {
		return "", issued, err
	}
	r.tokens = append(r.tokens, token)
	return token, issued, nil
}

func (r *wsResilienceRun) dial(p *wsPeer) error {
	token, _, err := r.sign(p.user, devTokenTTL)
	if err != nil {
		return err
	}
	return dialPeer(r.base, r.url, r.gatewayURL, token, p)
}

func (r *wsResilienceRun) connect() error {
	token, issued, err := r.sign(r.u.user, r.shortTTL)
	if err != nil {
		return err
	}
	r.issued = issued
	if err := dialPeer(r.base, r.url, r.gatewayURL, token, r.u); err != nil {
		return err
	}
	if err := r.dial(r.b); err != nil {
		return err
	}
	if r.room, err = groupOf(r.b, "e2e websocket resilience", r.u, r.b); err != nil {
		return err
	}
	if err := openAndBarrier(r.u, r.room); err != nil {
		return err
	}
	return r.sendHeard(1)
}

func (r *wsResilienceRun) sendHeard(seq uint64) error {
	mark := r.u.feed.Len()
	if err := sendText(r.b, r.room, seq, fmt.Sprintf("resilience %d", seq)); err != nil {
		return err
	}
	_, err := r.u.await(mark, e2e.CreatedMatch(r.room, seq))
	return err
}

func (r *wsResilienceRun) expired() error {
	i, err := r.u.awaitUntil(0, e2e.AuthRequiredMatch(chatimv1.AuthRequiredReason_AUTH_REQUIRED_REASON_EXPIRED), r.issued.Add(r.expiryWithin))
	if err != nil {
		return err
	}
	r.waitingFrom = i
	r.notes = append(r.notes, fmt.Sprintf("a %s token answered auth_required EXPIRED %s after signing", r.shortTTL, time.Since(r.issued).Round(100*time.Millisecond)))
	return nil
}

func (r *wsResilienceRun) silentWhileWaiting() error {
	mark := r.b.feed.Len()
	if err := sendText(r.b, r.room, 2, "resilience 2"); err != nil {
		return err
	}
	if _, err := r.b.await(mark, e2e.CreatedMatch(r.room, 2)); err != nil {
		return err
	}
	if !backoff.Pause(r.base, waitingQuiet) {
		return r.base.Err()
	}
	if err := e2e.CheckSilentWhileWaiting(r.u.feed.Entries(r.waitingFrom)); err != nil {
		return fmt.Errorf("%s: %w", r.u.name, err)
	}
	if _, err := r.u.call(markReadRequest(r.room, 2)); status.Code(err) != codes.Unauthenticated {
		return fmt.Errorf("%s: MarkRead while waiting = %w, want Unauthenticated", r.u.name, err)
	}
	return nil
}

func (r *wsResilienceRun) relogin() error {
	token, _, err := r.sign(r.u.user, devTokenTTL)
	if err != nil {
		return err
	}
	mark := r.u.feed.Len()
	if err := r.u.ws.Auth(token); err != nil {
		return fmt.Errorf("%s sends a fresh token: %w", r.u.name, err)
	}
	if _, err := r.u.await(mark, e2e.WSMatch{Frame: e2e.FrameReady}); err != nil {
		return err
	}
	if err := openAndBarrier(r.u, r.room); err != nil {
		return err
	}
	if err := r.sendHeard(3); err != nil {
		return err
	}
	if err := e2e.CheckMissedWhileWaiting(r.u.feed.Entries(mark), r.room, 2); err != nil {
		return fmt.Errorf("%s: %w", r.u.name, err)
	}
	r.notes = append(r.notes, "ready on a fresh token, seq 2 sent while waiting reported as a gap, seq 3 heard")
	return nil
}

func groupOf(creator *wsPeer, name string, members ...*wsPeer) (string, error) {
	users := make([]string, 0, len(members))
	for _, p := range members {
		users = append(users, p.user)
	}
	resp, err := viaGateway(creator.gw, creator.gw.core.CreateRoom, &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: name, Members: users, RequestId: randomCID(),
	})
	if err != nil {
		return "", fmt.Errorf("create room: %w", err)
	}
	room := resp.GetRoom().GetId()
	if room == "" {
		return "", errors.New("create room returned no room id")
	}
	for _, p := range members {
		if _, err := p.await(0, e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "member_added", Room: room, User: p.user}); err != nil {
			return "", err
		}
	}
	return room, nil
}

func openAndBarrier(p *wsPeer, room string) error {
	if err := p.ws.OpenRoom(room); err != nil {
		return fmt.Errorf("%s opens %s: %w", p.name, room, err)
	}
	return p.barrier()
}

func sendText(p *wsPeer, room string, want uint64, text string) error {
	resp, err := p.call(sendRequest(&chatimv1.SendMessageRequest{RoomId: room, Cid: randomCID(), Text: text}))
	if err != nil {
		return fmt.Errorf("%s sends %q: %w", p.name, text, err)
	}
	if seq := resp.GetSendMessage().GetSeq(); seq != want {
		return fmt.Errorf("%s sent %q at seq %d, want %d", p.name, text, seq, want)
	}
	return nil
}
