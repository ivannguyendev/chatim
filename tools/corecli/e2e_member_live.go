package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

type liveFlags struct {
	url  *string
	root *string
}

func addLiveFlags(fs *flag.FlagSet) liveFlags {
	return liveFlags{
		url:  fs.String("nats", cmp.Or(os.Getenv("NATS_URL"), "nats://chatim-nats:4222"), "NATS URL (env NATS_URL)"),
		root: fs.String("live-root", "live", "live subject root, EVT_LIVE_ROOT of the cores"),
	}
}

func singleTokens(tokens ...string) error {
	for _, t := range tokens {
		if t == "" || strings.ContainsAny(t, ".*> ") {
			return errors.New("-tenant and -live-root must be single subject tokens")
		}
	}
	return nil
}

type liveFeed struct {
	nc     *nats.Conn
	sub    *nats.Subscription
	events []e2e.Event
}

func openLive(url, subject string) (*liveFeed, error) {
	nc, err := nats.Connect(url, nats.Name("chatim-corecli-e2e-members"), nats.MaxReconnects(-1))
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	sub, err := nc.SubscribeSync(subject)
	if err == nil {
		err = nc.FlushTimeout(flushTimeout)
	}
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("subscribe %s: %w", subject, err)
	}
	fmt.Fprintln(os.Stderr, "watching", subject)
	return &liveFeed{nc: nc, sub: sub}, nil
}

func (f *liveFeed) Close() { f.nc.Close() }

func (f *liveFeed) add(msg *nats.Msg) error {
	var ev chatimv1.Event
	if err := proto.Unmarshal(msg.Data, &ev); err != nil {
		return fmt.Errorf("decode event on %s: %w", msg.Subject, err)
	}
	if got, ok := e2e.EventOf(msg.Subject, &ev); ok {
		f.events = append(f.events, got)
	}
	return nil
}

func (f *liveFeed) await(ctx context.Context, rooms []string, wants []e2e.Want, wait time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		missing, err := e2e.CheckLive(rooms, wants, f.events)
		switch {
		case err != nil:
			return fmt.Errorf("live events: %w", err)
		case len(missing) == 0:
			return f.drain(rooms, wants)
		}
		msg, err := f.sub.NextMsgWithContext(ctx)
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("after %v live events still missing: %v", wait, missing)
		}
		if err != nil {
			return fmt.Errorf("next live event: %w", err)
		}
		if err := f.add(msg); err != nil {
			return err
		}
	}
}

func (f *liveFeed) drain(rooms []string, wants []e2e.Want) error {
	for {
		pending, _, err := f.sub.Pending()
		if err != nil || pending == 0 {
			return err
		}
		msg, err := f.sub.NextMsg(flushTimeout)
		if err != nil {
			return fmt.Errorf("next live event: %w", err)
		}
		if err := f.add(msg); err != nil {
			return err
		}
		if _, err := e2e.CheckLive(rooms, wants, f.events); err != nil {
			return fmt.Errorf("live events: %w", err)
		}
	}
}
