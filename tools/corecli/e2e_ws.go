package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ivannguyendev/chatim/internal/wstest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

const (
	wsCallAttempts = 5
	wsRetryPause   = 500 * time.Millisecond
	wsHistoryLimit = 10
)

var errWSFewEmojis = errors.New("the websocket lists no reaction emoji")

type wsRun struct {
	base        context.Context
	cl          *route.Client
	ws          *wstest.Conn
	gw          *gateway
	gatewayURL  string
	key         devtoken.KeyPair
	url         string
	tenant      string
	user        string
	room        string
	emoji       string
	seq         uint64
	tokens      []string
	callTimeout time.Duration
	silentClose time.Duration
}

func e2eWS(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e ws", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	url := fs.String("url", "ws://chatim-websocket-1:8080/ws", "websocket endpoint")
	gatewayURL := fs.String("gateway", defaultGatewayURL, "HTTPS gateway endpoint for reads and room management")
	keyFile := fs.String("key", "/keys/"+devtoken.PrivateFile, "private JWK written by dev-keys")
	silentClose := fs.Duration("heartbeat-timeout", 15*time.Second, "WS_HEARTBEAT_TIMEOUT of the websocket node")
	callTimeout := fs.Duration("call-timeout", 30*time.Second, "timeout of one websocket request")
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
	r := &wsRun{key: key, url: *url, gatewayURL: *gatewayURL, tenant: st.Tenant, user: "e2e-ws-" + st.Room, callTimeout: *callTimeout, silentClose: *silentClose}
	o.tenant, o.user = r.tenant, r.user
	err = withSession(ctx, o, func(ctx context.Context, s *session) error {
		r.base, r.cl = ctx, s.client
		return r.run()
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "websocket phase ok on group %s: seq %d sent, edited, reacted %s and bookmarked; reads match gRPC; %d tokens checked\n", r.room, r.seq, r.emoji, len(r.tokens))
	return e2e.WriteTokens(filepath.Join(*dir, "ws-tokens"), r.tokens...)
}

func (r *wsRun) run() error {
	steps := []func() error{
		r.connect, r.createRoom, r.send, r.historySees, r.edit, r.react, r.bookmark, r.matchGRPC,
		r.systemRefused, r.rogueKeyRefused, r.expiredRefused, r.silentClosed,
	}
	defer r.close()
	for i, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("websocket step %d: %w", i+1, err)
		}
		fmt.Fprintf(os.Stderr, "websocket step %d ok\n", i+1)
	}
	return nil
}

func (r *wsRun) close() {
	if r.ws != nil {
		_ = r.ws.Close()
	}
}

func (r *wsRun) sign(key devtoken.KeyPair, issued time.Time, ttl time.Duration) (string, error) {
	token, err := devSign(key, r.tenant, r.user, issued, ttl)
	if err != nil {
		return "", err
	}
	r.tokens = append(r.tokens, token)
	return token, nil
}

func (r *wsRun) dial(token string) (*wstest.Conn, error) {
	c, err := dialWS(r.base, r.url, token, r.callTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", r.url, err)
	}
	return c, nil
}

func (r *wsRun) connect() error {
	token, err := r.sign(r.key, time.Now(), devTokenTTL)
	if err != nil {
		return err
	}
	r.gw = newGateway(r.base, r.gatewayURL, token, r.callTimeout)
	r.ws, err = r.dial(token)
	return err
}

func (r *wsRun) call(req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, error) {
	return callWS(r.base, r.ws, r.callTimeout, req)
}

func (r *wsRun) createRoom() error {
	resp, err := viaGateway(r.gw, r.gw.core.CreateRoom, &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "e2e websocket", Members: []string{r.user}, RequestId: randomCID(),
	})
	if err != nil {
		return fmt.Errorf("create room: %w", err)
	}
	if r.room = resp.GetRoom().GetId(); r.room == "" {
		return errors.New("create room returned no room id")
	}
	return nil
}

func (r *wsRun) send() error {
	resp, err := r.call(sendRequest(&chatimv1.SendMessageRequest{RoomId: r.room, Cid: randomCID(), Text: wsSentText}))
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if r.seq = resp.GetSendMessage().GetSeq(); r.seq != 1 {
		return fmt.Errorf("send returned seq %d, want 1", r.seq)
	}
	return nil
}

func (r *wsRun) historySees() error {
	resp, err := r.history()
	if err != nil {
		return err
	}
	return e2e.CheckWSMessage(e2e.MessageAt(resp.GetMessages(), r.seq), e2e.WSMessage{Seq: r.seq, Sender: r.user, Text: wsSentText})
}

func (r *wsRun) history() (*chatimv1.GetHistoryResponse, error) {
	resp, err := viaGateway(r.gw, r.gw.core.GetHistory, r.historyRequest())
	if err != nil {
		return nil, fmt.Errorf("history through the gateway: %w", err)
	}
	return resp, nil
}

func (r *wsRun) historyRequest() *chatimv1.GetHistoryRequest {
	return &chatimv1.GetHistoryRequest{RoomId: r.room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: wsHistoryLimit}
}

func sendRequest(req *chatimv1.SendMessageRequest) *chatimv1.ClientRequest {
	return &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_SendMessage{SendMessage: req}}
}

func firstEmoji(emojis []string) (string, error) {
	if len(emojis) == 0 {
		return "", errWSFewEmojis
	}
	return emojis[0], nil
}
