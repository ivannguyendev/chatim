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
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

const (
	gatewayRenamed      = "e2e gateway renamed"
	gatewaySameAttempts = 10
	gatewaySamePause    = 500 * time.Millisecond
)

type gatewayRun struct {
	base       context.Context
	cl         *route.Client
	key        devtoken.KeyPair
	wsURL      string
	gatewayURL string
	origin     string
	tenant     string
	a          *gateway
	aUser      string
	b          *wsPeer
	cUser      string
	room       string
	seq        uint64
	timeout    time.Duration
	tokens     []string
	notes      []string
}

func e2eGateway(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e gateway", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	wsURL := fs.String("url", "ws://chatim-websocket-1:8080/ws", "websocket endpoint")
	gatewayURL := fs.String("gateway", defaultGatewayURL, "HTTPS gateway endpoint")
	origin := fs.String("origin", "http://localhost:3000", "a browser origin listed in GW_CORS_ORIGINS")
	keyFile := fs.String("key", "/keys/"+devtoken.PrivateFile, "private JWK written by dev-keys")
	callTimeout := fs.Duration("call-timeout", 30*time.Second, "timeout of one request")
	wait := fs.Duration("wait", 30*time.Second, "how long to wait for one expected websocket frame")
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
	r := &gatewayRun{
		key: key, wsURL: *wsURL, gatewayURL: strings.TrimRight(*gatewayURL, "/"), origin: *origin, tenant: st.Tenant, timeout: *callTimeout,
		aUser: "e2e-gwa-" + st.Room, cUser: "e2e-gwc-" + st.Room,
	}
	r.b = &wsPeer{name: "B", user: "e2e-gwb-" + st.Room, base: ctx, timeout: *callTimeout, wait: *wait}
	o.tenant, o.user = r.tenant, r.aUser
	err = withSession(ctx, o, func(ctx context.Context, s *session) error {
		r.base, r.cl = ctx, s.client
		return r.run()
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "gateway phase ok on group %s: %s\n", r.room, strings.Join(r.notes, "; "))
	return e2e.WriteTokens(filepath.Join(*dir, "gw-tokens"), r.tokens...)
}

func (r *gatewayRun) run() error {
	steps := []wsEventsStep{
		{"A gets a gateway token and B connects to the websocket", r.connect},
		{"A creates a group with B through the gateway and B hears member_added", r.createGroup},
		{"A adds C through the gateway and B, with the group open, hears member_added", r.addC},
		{"A renames the group through the gateway and B hears room_updated", r.rename},
		{"ListMyRooms, GetHistory, ListMembers and ListBookmarks through the gateway equal gRPC", r.readsMatch},
		{"B marks read on the websocket, flushes and lists its rooms through the gateway with that read_seq", r.flushedRead},
		{"an expired token gets 401", r.expiredRefused},
		{"SendMessage through the gateway is unimplemented", r.sendRefused},
		{"GetHistory through the websocket is unimplemented", r.b.barrier},
		{"a CORS preflight from a listed origin gets the CORS headers", r.preflight},
	}
	defer r.b.close()
	for i, step := range steps {
		if err := step.run(); err != nil {
			return fmt.Errorf("gateway step %d (%s): %w", i+1, step.name, err)
		}
		fmt.Fprintf(os.Stderr, "gateway step %d ok: %s\n", i+1, step.name)
	}
	return nil
}

func (r *gatewayRun) sign(user string, issued time.Time, ttl time.Duration) (string, error) {
	token, err := devSign(r.key, r.tenant, user, issued, ttl)
	if err != nil {
		return "", err
	}
	r.tokens = append(r.tokens, token)
	return token, nil
}

func (r *gatewayRun) connect() error {
	tokenA, err := r.sign(r.aUser, time.Now(), devTokenTTL)
	if err != nil {
		return err
	}
	r.a = newGateway(r.base, r.gatewayURL, tokenA, r.timeout)
	tokenB, err := r.sign(r.b.user, time.Now(), devTokenTTL)
	if err != nil {
		return err
	}
	return dialPeer(r.base, r.wsURL, r.gatewayURL, tokenB, r.b)
}

func (r *gatewayRun) inRoom(kind, user string) e2e.WSMatch {
	return e2e.WSMatch{Frame: e2e.FrameEvent, Kind: kind, Room: r.room, User: user}
}

func (r *gatewayRun) createGroup() error {
	resp, err := viaGateway(r.a, r.a.core.CreateRoom, &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "e2e gateway", Members: []string{r.aUser, r.b.user}, RequestId: randomCID(),
	})
	if err != nil {
		return fmt.Errorf("create room: %w", err)
	}
	if r.room = resp.GetRoom().GetId(); r.room == "" {
		return errors.New("create room returned no room id")
	}
	if _, err := r.b.await(0, r.inRoom("member_added", r.b.user)); err != nil {
		return err
	}
	if err := r.b.ws.OpenRoom(r.base, r.room); err != nil {
		return fmt.Errorf("peer B opens %s: %w", r.room, err)
	}
	return r.b.barrier()
}

func (r *gatewayRun) addC() error {
	mark := r.b.feed.Len()
	if _, err := viaGateway(r.a, r.a.core.AddMembers, &chatimv1.AddMembersRequest{RoomId: r.room, Users: []string{r.cUser}, RequestId: randomCID()}); err != nil {
		return fmt.Errorf("add %s: %w", r.cUser, err)
	}
	_, err := r.b.await(mark, r.inRoom("member_added", r.cUser))
	return err
}

func (r *gatewayRun) rename() error {
	mark := r.b.feed.Len()
	name := gatewayRenamed
	resp, err := viaGateway(r.a, r.a.core.UpdateRoom, &chatimv1.UpdateRoomRequest{RoomId: r.room, Name: &name})
	if err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	if !resp.GetChanged() || resp.GetRoom().GetName() != gatewayRenamed {
		return fmt.Errorf("rename returned changed %v name %q", resp.GetChanged(), resp.GetRoom().GetName())
	}
	_, err = r.b.await(mark, r.inRoom("room_updated", ""))
	return err
}

func (r *gatewayRun) pause() bool { return backoff.Pause(r.base, gatewaySamePause) }
