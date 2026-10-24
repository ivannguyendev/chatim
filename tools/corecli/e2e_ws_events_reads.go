package main

import (
	"fmt"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const readArrivalLimit = 3 * time.Second

func (r *wsEventsRun) readPeers() error {
	r.a2 = &wsPeer{name: "A2", user: r.a.user, base: r.base, timeout: r.a.timeout, wait: r.a.wait}
	r.c = &wsPeer{name: "C", user: "e2e-wsc-" + r.room, base: r.base, timeout: r.a.timeout, wait: r.a.wait}
	for _, p := range []*wsPeer{r.a2, r.c} {
		token, err := devSign(r.key, r.tenant, p.user, time.Now(), devTokenTTL)
		if err != nil {
			return err
		}
		r.tokens = append(r.tokens, token)
		if err := dialPeer(r.base, r.url, r.gatewayURL, token, p); err != nil {
			return err
		}
	}
	if _, err := viaGateway(r.a.gw, r.a.gw.core.AddMembers, &chatimv1.AddMembersRequest{
		RoomId: r.room, Users: []string{r.c.user}, RequestId: randomCID(),
	}); err != nil {
		return fmt.Errorf("add %s: %w", r.c.user, err)
	}
	added := r.inRoom("member_added")
	added.User = r.c.user
	_, err := r.c.await(0, added)
	return err
}

func (r *wsEventsRun) readSeen() error {
	marks := map[*wsPeer]int{r.b: r.b.feed.Len(), r.a2: r.a2.feed.Len(), r.c: r.c.feed.Len()}
	start := time.Now()
	if _, err := r.a.call(markReadRequest(r.room, 0)); err != nil {
		return fmt.Errorf("peer A marks read: %w", err)
	}
	read := r.readOf(0)
	i, err := r.b.await(marks[r.b], read)
	if err != nil {
		return err
	}
	delay := time.Since(start)
	if delay > readArrivalLimit {
		return fmt.Errorf("read_positions reached B after %v, want within %v", delay, readArrivalLimit)
	}
	first := r.b.feed.Entries(i)[0]
	if _, err := r.a2.await(marks[r.a2], read); err != nil {
		return err
	}
	if err := r.settle(); err != nil {
		return err
	}
	if err := r.c.absent(marks[r.c], e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "read_positions", Room: r.room}); err != nil {
		return err
	}
	r.readSeq, r.readVer = first.Seq, first.Ver
	r.notes = append(r.notes, fmt.Sprintf("read_positions A->B in %v (read_seq %d read_ver %d)", delay.Round(time.Millisecond), first.Seq, first.Ver))
	return nil
}

func (r *wsEventsRun) unreadSeen() error {
	markB := r.b.feed.Len()
	if _, err := r.a.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_MarkUnread{MarkUnread: &chatimv1.MarkUnreadRequest{RoomId: r.room, Seq: r.readSeq}}}); err != nil {
		return fmt.Errorf("peer A marks unread from %d: %w", r.readSeq, err)
	}
	i, err := r.b.await(markB, r.readOf(r.readVer+1))
	if err != nil {
		return err
	}
	if got := r.b.feed.Entries(i)[0]; got.Seq != r.readSeq-1 {
		return fmt.Errorf("peer B got read_seq %d of A after the unread, want %d", got.Seq, r.readSeq-1)
	}
	return nil
}

func (r *wsEventsRun) readOf(ver uint64) e2e.WSMatch {
	return e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "read_positions", Room: r.room, User: r.a.user, Ver: ver}
}
