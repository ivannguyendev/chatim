package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

const drillHistoryPage = 100

type wsDrill struct {
	base     context.Context
	sender   *wsPeer
	receiver *wsPeer
	leaver   *wsPeer
	room     string
	rate     int
	start    time.Time
	acked    uint64
	failed   int
	lastSeq  uint64
	reported int
	removal  drillRemoval
	resyncs  int
}

func e2eWSDrill(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e ws-drill", flag.ContinueOnError)
	url := fs.String("url", "ws://chatim-websocket-1:8080/ws", "websocket endpoint")
	keyFile := fs.String("key", "/keys/"+devtoken.PrivateFile, "private JWK written by dev-keys")
	tenant := fs.String("tenant", "drill", "tenant of the drill users")
	duration := fs.Duration("duration", 5*time.Minute, "how long to send")
	rate := fs.Int("rate", 20, "messages per second sent by one websocket connection, below WS_COMMAND_RATE")
	grace := fs.Duration("grace", time.Minute, "after the last send, wait this long for the receiver to see the last seq")
	callTimeout := fs.Duration("call-timeout", 10*time.Second, "timeout of one websocket request")
	removeAfter := fs.Duration("remove-after", 0, "remove the leaver from the group this long after the sends start; 0 keeps it")
	resyncs := fs.Int("expect-resyncs", -1, "resync frames every connection must receive; -1 skips the check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	key, err := devtoken.LoadPrivateFile(*keyFile)
	if err != nil {
		return err
	}
	run := randomCID()[:8]
	d := &wsDrill{base: ctx, rate: max(*rate, 1), removal: drillRemoval{after: *removeAfter}, resyncs: *resyncs}
	d.sender = &wsPeer{name: "sender", user: "drill-s-" + run, base: ctx, timeout: *callTimeout, wait: *grace}
	d.receiver = &wsPeer{name: "receiver", user: "drill-r-" + run, base: ctx, timeout: *callTimeout, wait: *grace}
	d.leaver = &wsPeer{name: "leaver", user: "drill-l-" + run, base: ctx, timeout: *callTimeout, wait: *grace}
	defer d.sender.close()
	defer d.receiver.close()
	defer d.leaver.close()
	for _, p := range d.peers() {
		token, err := devSign(key, *tenant, p.user, time.Now(), *duration+*grace+devTokenTTL)
		if err != nil {
			return err
		}
		if err := dialPeer(ctx, *url, token, p); err != nil {
			return err
		}
	}
	if err := d.prepare(); err != nil {
		return err
	}
	d.sendFor(*duration)
	d.awaitLast()
	return d.report()
}

func (d *wsDrill) prepare() error {
	resp, err := d.sender.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_CreateRoom{CreateRoom: &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "ws drill", Members: []string{d.sender.user, d.receiver.user, d.leaver.user}, RequestId: randomCID(),
	}}})
	if err != nil {
		return fmt.Errorf("create room: %w", err)
	}
	d.room = resp.GetCreateRoom().GetRoom().GetId()
	for _, p := range []*wsPeer{d.receiver, d.leaver} {
		if _, err := p.await(0, e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "member_added", Room: d.room, User: p.user}); err != nil {
			return err
		}
		if err := p.ws.OpenRoom(d.base, d.room); err != nil {
			return fmt.Errorf("%s opens %s: %w", p.name, d.room, err)
		}
	}
	fmt.Fprintf(os.Stderr, "drill room %s, sender %s, receiver %s, leaver %s, %d msg/s\n", d.room, d.sender.user, d.receiver.user, d.leaver.user, d.rate)
	return nil
}

func (d *wsDrill) sendFor(duration time.Duration) {
	d.start = time.Now()
	tick := time.NewTicker(time.Second / time.Duration(d.rate))
	defer tick.Stop()
	progress := time.NewTicker(time.Second)
	defer progress.Stop()
	end := time.After(duration)
	remove := d.removal.timer()
	for {
		select {
		case <-remove:
			d.removeLeaver()
		case <-tick.C:
			d.sendOne()
		case <-progress.C:
			d.progress()
		case <-end:
			return
		case <-d.base.Done():
			return
		}
	}
}

func (d *wsDrill) sendOne() {
	resp, err := d.sender.call(sendRequest(&chatimv1.SendMessageRequest{RoomId: d.room, Cid: randomCID(), Text: "ws drill"}))
	if err != nil {
		d.failed++
		return
	}
	d.acked++
	d.lastSeq = max(d.lastSeq, resp.GetSendMessage().GetSeq())
}

func (d *wsDrill) progress() {
	elapsed := time.Since(d.start).Round(time.Second)
	entries := d.receiver.feed.Entries(d.reported)
	d.reported += len(entries)
	for _, e := range entries {
		if e.Frame == e2e.FrameResync || e.Frame == e2e.FrameGap || e.Frame == e2e.FrameRoomClosed {
			fmt.Fprintf(os.Stderr, "+%v receiver %s %s seq %d..%d\n", elapsed, e.Frame, e.Kind, e.Seq, e.To)
		}
		if e.Frame == e2e.FrameResync {
			d.reopen()
		}
	}
	if int(elapsed.Seconds())%10 == 0 {
		live := d.receiver.feed.Count(e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "message_created", Room: d.room})
		fmt.Fprintf(os.Stderr, "+%v acked %d (last seq %d), failed %d, receiver live %d\n", elapsed, d.acked, d.lastSeq, d.failed, live)
	}
}

func (d *wsDrill) awaitLast() {
	if d.lastSeq == 0 {
		return
	}
	at := time.Now()
	if _, err := d.receiver.await(0, e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "message_created", ID: e2e.MessageEventID(d.room, d.lastSeq)}); err != nil {
		fmt.Fprintf(os.Stderr, "receiver never saw the last seq %d: %v\n", d.lastSeq, err)
		return
	}
	fmt.Fprintf(os.Stderr, "receiver saw the last seq %d %v after the sends ended\n", d.lastSeq, time.Since(at).Round(time.Millisecond))
}

func (d *wsDrill) report() error {
	d.progress()
	stored, err := d.history()
	if err != nil {
		return err
	}
	var live []uint64
	entries := d.receiver.feed.Entries(0)
	for _, e := range entries {
		if e.Frame == e2e.FrameEvent && e.Kind == "message_created" && e.Room == d.room {
			live = append(live, e.Seq)
		}
	}
	loss := e2e.CheckDrillLoss(stored, live, entries)
	stats := d.receiver.applier.Stats()
	fmt.Fprintf(os.Stderr, "drill: acked %d, failed %d; %v; receiver applier %+v, connection open %v\n",
		d.acked, d.failed, loss, stats, d.receiver.ws.Err() == nil)
	if err := loss.Err(); err != nil {
		return err
	}
	if stats.Duplicates != 0 {
		return fmt.Errorf("receiver applier dropped %d duplicates", stats.Duplicates)
	}
	return errors.Join(d.checkResyncs(), d.checkLeaver())
}

func (d *wsDrill) history() ([]uint64, error) {
	var stored []uint64
	req := &chatimv1.GetHistoryRequest{RoomId: d.room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST, Limit: drillHistoryPage}
	for {
		resp, err := d.receiver.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_GetHistory{GetHistory: req}})
		if err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		msgs := resp.GetGetHistory().GetMessages()
		next := req.GetSeq()
		for _, m := range msgs {
			stored = append(stored, m.GetSeq())
			next = max(next, m.GetSeq())
		}
		if len(msgs) == 0 || next == req.GetSeq() {
			return stored, nil
		}
		req = &chatimv1.GetHistoryRequest{RoomId: d.room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_AFTER, Seq: next, Limit: drillHistoryPage}
	}
}
