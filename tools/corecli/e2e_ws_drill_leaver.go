package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

var errLeaverKept = errors.New("the sends ended before the leaver was removed")

type drillRemoval struct {
	after      time.Duration
	done       bool
	lastBefore uint64
	err        error
}

func (r drillRemoval) timer() <-chan time.Time {
	if r.after <= 0 {
		return nil
	}
	return time.After(r.after)
}

func (d *wsDrill) peers() []*wsPeer {
	return []*wsPeer{d.sender, d.receiver, d.leaver}
}

func (d *wsDrill) reopen() {
	if err := d.receiver.ws.OpenRoom(d.room); err != nil {
		fmt.Fprintf(os.Stderr, "receiver reopens %s after resync: %v\n", d.room, err)
	}
}

func (d *wsDrill) removeLeaver() {
	d.removal.done, d.removal.lastBefore = true, d.lastSeq
	_, d.removal.err = viaGateway(d.sender.gw, d.sender.gw.core.RemoveMember, &chatimv1.RemoveMemberRequest{
		RoomId: d.room, User: d.leaver.user,
	})
	fmt.Fprintf(os.Stderr, "+%v removed %s from %s after seq %d: err %v\n",
		time.Since(d.start).Round(time.Second), d.leaver.user, d.room, d.removal.lastBefore, d.removal.err)
}

func (d *wsDrill) checkResyncs() error {
	var errs []error
	for _, p := range d.peers() {
		n := p.feed.Count(e2e.WSMatch{Frame: e2e.FrameResync})
		fmt.Fprintf(os.Stderr, "drill: %s received %d resync, connection open %v\n", p.name, n, p.ws.Err() == nil)
		if d.resyncs >= 0 && n != d.resyncs {
			errs = append(errs, fmt.Errorf("%s received %d resync frames, want %d", p.name, n, d.resyncs))
		}
	}
	return errors.Join(errs...)
}

func (d *wsDrill) checkLeaver() error {
	if d.removal.after <= 0 {
		return nil
	}
	if !d.removal.done {
		return errLeaverKept
	}
	if d.removal.err != nil {
		return fmt.Errorf("remove the leaver: %w", d.removal.err)
	}
	c := e2e.CheckRemoved(d.leaver.feed.Entries(0), d.room, d.leaver.user, d.removal.lastBefore)
	fmt.Fprintf(os.Stderr, "drill: leaver removed after seq %d: %v\n", d.removal.lastBefore, c)
	return c.Err()
}
