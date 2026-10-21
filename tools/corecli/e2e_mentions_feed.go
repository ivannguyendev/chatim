package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

type noticeFeed struct {
	nc  *nats.Conn
	sub *nats.Subscription
	got []e2e.Notice
}

func openNotices(url, subject string) (*noticeFeed, error) {
	nc, sub, err := subscribe(url, "chatim-corecli-e2e-mentions", subject)
	if err != nil {
		return nil, err
	}
	return &noticeFeed{nc: nc, sub: sub}, nil
}

func (f *noticeFeed) Close() { f.nc.Close() }

func (f *noticeFeed) add(msg *nats.Msg) error {
	var d chatimv1.WsPublish
	if err := proto.Unmarshal(msg.Data, &d); err != nil {
		return fmt.Errorf("decode ws publish packet on %s: %w", msg.Subject, err)
	}
	f.got = append(f.got, e2e.NoticesOf(msg.Subject, &d)...)
	return nil
}

func (f *noticeFeed) await(ctx context.Context, room string, wants []e2e.Notice, wait time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		missing, err := e2e.CheckNotices(room, wants, f.got)
		switch {
		case err != nil:
			return fmt.Errorf("notifications: %w", err)
		case len(missing) == 0:
			return f.drain(room, wants)
		}
		msg, err := f.sub.NextMsgWithContext(ctx)
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("after %v notifications still missing: %v", wait, missing)
		}
		if err != nil {
			return fmt.Errorf("next notification: %w", err)
		}
		if err := f.add(msg); err != nil {
			return err
		}
	}
}

func (f *noticeFeed) drain(room string, wants []e2e.Notice) error {
	for {
		pending, _, err := f.sub.Pending()
		if err != nil || pending == 0 {
			return err
		}
		msg, err := f.sub.NextMsg(flushTimeout)
		if err != nil {
			return fmt.Errorf("next notification: %w", err)
		}
		if err := f.add(msg); err != nil {
			return err
		}
		if _, err := e2e.CheckNotices(room, wants, f.got); err != nil {
			return fmt.Errorf("notifications: %w", err)
		}
	}
}
