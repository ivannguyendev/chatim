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

var errWSTwoEmojis = errors.New("the websocket lists fewer than two reaction emojis")

type wsEventsRun struct {
	base             context.Context
	cl               *route.Client
	key              devtoken.KeyPair
	url              string
	tenant           string
	quiet            time.Duration
	a, b             *wsPeer
	a2, c            *wsPeer
	room             string
	readSeq, readVer uint64
	seq              uint64
	tokens           []string
	notes            []string
}

type wsEventsStep struct {
	name string
	run  func() error
}

func e2eWSEvents(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e ws-events", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	url := fs.String("url", "ws://chatim-websocket-1:8080/ws", "websocket endpoint")
	keyFile := fs.String("key", "/keys/"+devtoken.PrivateFile, "private JWK written by dev-keys")
	callTimeout := fs.Duration("call-timeout", 30*time.Second, "timeout of one websocket request")
	wait := fs.Duration("wait", 30*time.Second, "how long to wait for one expected frame")
	quiet := fs.Duration("quiet", time.Second, "how long a peer must stay silent after the barrier frame reached the other")
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
	r := &wsEventsRun{base: ctx, key: key, url: *url, tenant: st.Tenant, quiet: *quiet}
	r.a = &wsPeer{name: "A", user: "e2e-wsa-" + st.Room, base: ctx, timeout: *callTimeout, wait: *wait}
	r.b = &wsPeer{name: "B", user: "e2e-wsb-" + st.Room, base: ctx, timeout: *callTimeout, wait: *wait}
	o.tenant = r.tenant
	err = withRoutes(ctx, o, func(_ context.Context, s *session) error {
		r.cl = s.client
		return r.run()
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "websocket events ok on group %s: %s\n", r.room, strings.Join(r.notes, "; "))
	return e2e.WriteTokens(filepath.Join(*dir, "ws-events-tokens"), r.tokens...)
}

func (r *wsEventsRun) run() error {
	steps := []wsEventsStep{
		{"A and B connect and get ready", r.connect},
		{"A creates a group alone and B hears nothing of it", r.createGroup},
		{"A adds B and B gets member_added at once", r.addB},
		{"A sends and B gets msg_created", r.sendSeen},
		{"B gets no reaction_changed with the room closed and the next one after open_room", r.reactions},
		{"A hides and bookmarks and only A hears it", r.ownerOnly},
		{"@B and @all reach B as one notification each", r.mentions},
		{"A's second device and C connect and C joins the group", r.readPeers},
		{"A's MarkRead reaches B viewing the group and A's second device, not C", r.readSeen},
		{"A's MarkUnread reaches B with a higher read_ver and a lower read_seq", r.unreadSeen},
		{"A removes B, B gets member_removed then room_closed and nothing more", r.removeB},
		{"core lists the group in B's left rooms with its version and not in B's rooms", r.leftListed},
		{"B reconnects and hears nothing of the group it left", r.reconnectB},
		{"no duplicate, gap or resync on either applier", r.clean},
	}
	defer r.a.close()
	defer r.b.close()
	defer func() {
		for _, p := range []*wsPeer{r.a2, r.c} {
			if p != nil {
				p.close()
			}
		}
	}()
	for i, step := range steps {
		if err := step.run(); err != nil {
			return fmt.Errorf("websocket events step %d (%s): %w", i+1, step.name, err)
		}
		fmt.Fprintf(os.Stderr, "websocket events step %d ok: %s\n", i+1, step.name)
	}
	return nil
}

func (r *wsEventsRun) connect() error {
	for _, p := range []*wsPeer{r.a, r.b} {
		token, err := devSign(r.key, r.tenant, p.user, time.Now(), devTokenTTL)
		if err != nil {
			return err
		}
		r.tokens = append(r.tokens, token)
		if err := dialPeer(r.base, r.url, token, p); err != nil {
			return err
		}
	}
	return nil
}

func (r *wsEventsRun) settle() error {
	if !backoff.Pause(r.base, r.quiet) {
		return r.base.Err()
	}
	return nil
}

func (r *wsEventsRun) send(text string, mentions *chatimv1.MentionSet) (uint64, error) {
	resp, err := r.a.call(sendRequest(&chatimv1.SendMessageRequest{RoomId: r.room, Cid: randomCID(), Text: text, Mentions: mentions}))
	if err != nil {
		return 0, fmt.Errorf("send %q: %w", text, err)
	}
	return resp.GetSendMessage().GetSeq(), nil
}

func (r *wsEventsRun) created(seq uint64) e2e.WSMatch {
	return e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "message_created", ID: e2e.MessageEventID(r.room, seq)}
}

func (r *wsEventsRun) inRoom(kind string) e2e.WSMatch {
	return e2e.WSMatch{Frame: e2e.FrameEvent, Kind: kind, Room: r.room}
}

func (r *wsEventsRun) once(p *wsPeer, m e2e.WSMatch) error {
	if n := p.feed.Count(m); n != 1 {
		return fmt.Errorf("%s received %d frames matching %v, want exactly 1", p.name, n, m)
	}
	return nil
}

func (r *wsEventsRun) clean() error {
	for _, p := range []*wsPeer{r.a, r.b} {
		stats := p.applier.Stats()
		if err := e2e.CheckApplierStats(p.name, stats); err != nil {
			return err
		}
		if err := p.absent(0, e2e.WSMatch{Frame: e2e.FrameResync}); err != nil {
			return err
		}
		r.notes = append(r.notes, fmt.Sprintf("%s applier %d delivered, 0 duplicates, 0 gaps, %d stale", p.name, stats.Delivered, stats.Stale))
	}
	return nil
}
