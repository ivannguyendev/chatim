package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/route"
)

const eventPoll = 200 * time.Millisecond

func e2eCheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e check", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	events := fs.String("events", "/state/events.jsonl", "events file written by corecli watch -out")
	limit := fs.Int("page", 16, "history page size, at most 100")
	wait := fs.Duration("wait", 45*time.Second, "wait this long for a live event of every acked pts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 1 || *limit > 100 {
		return fmt.Errorf("-page %d: want 1..100", *limit)
	}
	size := int32(*limit)
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	if err := e2e.CheckAcks(st.Acks); err != nil {
		return fmt.Errorf("acks: %w", err)
	}
	o.tenant, o.user = st.Tenant, st.User
	err = withSession(ctx, o, func(ctx context.Context, s *session) error {
		return checkHistory(ctx, s.client, st, size)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "history ok: %d messages, seq 1..%d on LATEST, OLDEST, AFTER and BEFORE pages\n", len(st.Acks), len(st.Acks))
	cov, err := awaitEvents(ctx, st, *events, *wait)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "live ok: an event for each of %d pts, %d duplicate(s) dropped\n", cov.Distinct, cov.Duplicates)
	return nil
}

func checkHistory(ctx context.Context, cl *route.Client, st e2e.State, limit int32) error {
	n, size := len(st.Acks), int(limit)
	latest, err := page(ctx, cl, st.Room, chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, 0, limit)
	if err == nil {
		err = e2e.CheckPage(st.Acks[max(0, n-size):], latest, st.Room, st.User)
	}
	if err != nil {
		return fmt.Errorf("LATEST page: %w", err)
	}
	oldest, err := page(ctx, cl, st.Room, chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST, 0, limit)
	if err == nil {
		err = e2e.CheckPage(st.Acks[:min(n, size)], oldest, st.Room, st.User)
	}
	if err != nil {
		return fmt.Errorf("OLDEST page: %w", err)
	}
	for _, forward := range []bool{true, false} {
		all, err := walk(ctx, cl, st.Room, limit, n/size+2, forward)
		if err == nil {
			err = e2e.CheckPage(st.Acks, all, st.Room, st.User)
		}
		if err != nil {
			return fmt.Errorf("history walk (forward %v): %w", forward, err)
		}
	}
	return nil
}

func walk(ctx context.Context, cl *route.Client, room string, limit int32, pages int, forward bool) ([]*chatimv1.Message, error) {
	anchor, next := chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, chatimv1.HistoryAnchor_HISTORY_ANCHOR_BEFORE
	if forward {
		anchor, next = chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST, chatimv1.HistoryAnchor_HISTORY_ANCHOR_AFTER
	}
	var all []*chatimv1.Message
	var seq uint64
	for range pages {
		msgs, err := page(ctx, cl, room, anchor, seq, limit)
		if err != nil || len(msgs) == 0 {
			return all, err
		}
		anchor = next
		if forward {
			all, seq = append(all, msgs...), msgs[len(msgs)-1].GetSeq()
		} else {
			all, seq = append(slices.Clone(msgs), all...), msgs[0].GetSeq()
		}
	}
	return nil, fmt.Errorf("walk did not end within %d pages", pages)
}

func page(ctx context.Context, cl *route.Client, room string, anchor chatimv1.HistoryAnchor, seq uint64, limit int32) ([]*chatimv1.Message, error) {
	resp, _, err := cl.GetHistory(ctx, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: anchor, Seq: seq, Limit: limit})
	return resp.GetMessages(), err
}

func awaitEvents(ctx context.Context, st e2e.State, path string, wait time.Duration) (e2e.Coverage, error) {
	deadline := time.Now().Add(wait)
	for {
		evs, err := e2e.ReadEvents(path)
		if err != nil {
			return e2e.Coverage{}, err
		}
		cov, err := e2e.CheckEvents(st.Acks, st.Room, evs)
		switch {
		case err != nil:
			return cov, fmt.Errorf("live events: %w", err)
		case cov.MissingCount == 0:
			return cov, nil
		case time.Now().After(deadline):
			return cov, fmt.Errorf("%d of %d pts have no live event after %v, first missing %v", cov.MissingCount, len(st.Acks), wait, cov.Missing)
		case !backoff.Pause(ctx, eventPoll):
			return cov, ctx.Err()
		}
	}
}
