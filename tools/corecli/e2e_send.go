package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

type sendTally struct {
	sent, attempts, retried, maxAttempts int
	slowest                              time.Duration
	byAddr                               map[string]int
}

func (t *sendTally) add(st route.Stats, took time.Duration) {
	t.sent++
	t.attempts += st.Attempts
	t.maxAttempts = max(t.maxAttempts, st.Attempts)
	t.slowest = max(t.slowest, took)
	if st.Attempts > 1 {
		t.retried++
	}
	t.byAddr[st.Addr]++
}

func (t *sendTally) String() string {
	var served []string
	for _, addr := range slices.Sorted(maps.Keys(t.byAddr)) {
		served = append(served, fmt.Sprintf("%s=%d", addr, t.byAddr[addr]))
	}
	return fmt.Sprintf("%d sends, %d attempts, %d retried, max %d attempts, slowest %v, served by %s",
		t.sent, t.attempts, t.retried, t.maxAttempts, t.slowest.Round(time.Millisecond), strings.Join(served, " "))
}

func e2eSend(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e send", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	count := fs.Int("count", 20, "messages to send")
	prefix := fs.String("prefix", "m", "cid prefix, unique per phase")
	resend := fs.Bool("resend-last", false, "first resend the last acked cid and expect its original ack")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	o.tenant, o.user = st.Tenant, st.User
	tally := &sendTally{byAddr: map[string]int{}}
	err = withSession(ctx, o, func(ctx context.Context, s *session) error {
		if *resend {
			if err := resendLast(ctx, s.client, st, tally); err != nil {
				return err
			}
		}
		first := len(st.Acks) + 1
		for i := range *count {
			ack, err := sendOne(ctx, s.client, st.Room, fmt.Sprintf("%s-%d", *prefix, i+1), tally)
			if err != nil {
				return err
			}
			st.Acks = append(st.Acks, ack)
			if err := e2e.CheckAcks(st.Acks); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "sent seq %d..%d: %v\n", first, len(st.Acks), tally)
		return nil
	})
	return errors.Join(err, e2e.Save(statePath(*dir), st))
}

func sendOne(ctx context.Context, cl *route.Client, room, cid string, tally *sendTally) (e2e.Ack, error) {
	start := time.Now()
	resp, stats, err := cl.SendMessage(ctx, &chatimv1.SendMessageRequest{RoomId: room, Cid: cid, Text: e2e.TextFor(cid)})
	tally.add(stats, time.Since(start))
	if err != nil {
		return e2e.Ack{}, fmt.Errorf("send cid %s: %w", cid, err)
	}
	return e2e.Ack{CID: cid, Seq: resp.GetSeq()}, nil
}

func resendLast(ctx context.Context, cl *route.Client, st e2e.State, tally *sendTally) error {
	if len(st.Acks) == 0 {
		return errors.New("-resend-last needs an acked message")
	}
	last := st.Acks[len(st.Acks)-1]
	got, err := sendOne(ctx, cl, st.Room, last.CID, tally)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	if got != last {
		return fmt.Errorf("resend of acked cid %s returned seq %d, want the original seq %d", last.CID, got.Seq, last.Seq)
	}
	fmt.Fprintf(os.Stderr, "resent cid %s: same ack seq %d\n", last.CID, got.Seq)
	return nil
}
