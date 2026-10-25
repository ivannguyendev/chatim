package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/internal/wstest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/poc/internal/wsload"
)

const (
	dialTimeout       = 10 * time.Second
	loginTimeout      = 2 * time.Minute
	reconnectMaxDelay = 3 * time.Second
)

var errLoginTimeout = errors.New("no ready within the login timeout")

type client struct {
	l     *load
	user  string
	token string
	room  string
	node  int
	conn  atomic.Pointer[wstest.Conn]
	seen  wsload.SeqSet
	first chan struct{}
}

func (c *client) run(ctx context.Context) {
	started := time.Now()
	conn, err := c.login(ctx)
	c.l.loggedIn(started, err)
	close(c.first)
	if err != nil {
		return
	}
	for {
		c.receive(ctx, conn)
		c.conn.Store(nil)
		if ctx.Err() != nil {
			return
		}
		c.l.errs.add("dropped", conn.Err())
		if !c.l.reconnect {
			return
		}
		c.l.drops.dropped()
		if conn = c.relogin(ctx); conn == nil {
			return
		}
		c.l.drops.recovered()
	}
}

func (c *client) relogin(ctx context.Context) *wstest.Conn {
	for {
		if !sleep(ctx, nil, wsload.ReconnectDelay(reconnectMaxDelay, rand.Int64N)) {
			return nil
		}
		c.node = wsload.OtherURL(len(c.l.cfg.urls), c.node, rand.IntN)
		conn, err := c.login(ctx)
		if err == nil {
			return conn
		}
		if ctx.Err() != nil {
			return nil
		}
		c.l.errs.add("relogin", err)
	}
}

func (c *client) login(ctx context.Context) (*wstest.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	conn, err := wstest.Dial(dialCtx, c.l.cfg.urls[c.node])
	cancel()
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	if err := c.awaitReady(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.OpenRoom(c.room); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open room: %w", err)
	}
	c.conn.Store(conn)
	return conn, nil
}

func (c *client) awaitReady(ctx context.Context, conn *wstest.Conn) error {
	timeout := time.NewTimer(loginTimeout)
	defer timeout.Stop()
	if err := conn.Auth(c.token); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return errLoginTimeout
		case frame, ok := <-conn.Frames():
			if !ok {
				return conn.Err()
			}
			switch {
			case frame.GetReady() != nil:
				return nil
			case frame.GetRetryLater() != nil:
				c.l.retryLater.Add(1)
				if !sleep(ctx, conn, millis(frame.GetRetryLater().GetRetryAfterMs())) {
					return closedOr(ctx, conn)
				}
				if err := conn.Auth(c.token); err != nil {
					return fmt.Errorf("auth again: %w", err)
				}
			case frame.GetAuthRequired() != nil:
				return fmt.Errorf("auth_required %s", frame.GetAuthRequired().GetReason())
			case frame.GetLoadFailed() != nil:
				c.loadFailed(conn, frame.GetLoadFailed())
			}
		}
	}
}

func (c *client) receive(ctx context.Context, conn *wstest.Conn) {
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close()
			return
		case frame, ok := <-conn.Frames():
			if !ok {
				return
			}
			c.handle(conn, frame)
		}
	}
}

func (c *client) handle(conn *wstest.Conn, frame *chatimv1.ServerFrame) {
	switch {
	case frame.GetEvent().GetMessageCreated() != nil:
		c.delivered(frame.GetEvent().GetMessageCreated().GetMessage())
	case frame.GetLoadFailed() != nil:
		c.loadFailed(conn, frame.GetLoadFailed())
	case frame.GetResync() != nil:
		c.l.resyncs.Add(1)
	}
}

func (c *client) delivered(m *chatimv1.Message) {
	sent, ok := wsload.ParseStamp(c.l.run, m.GetText())
	if !ok || m.GetRoomId() != c.room {
		return
	}
	if !c.seen.Add(m.GetSeq()) {
		c.l.duplicates.Add(1)
		return
	}
	c.l.delivered.Add(1)
	c.l.delivery.Add(time.Since(sent))
}

func (c *client) loadFailed(conn *wstest.Conn, f *chatimv1.LoadFailedFrame) {
	c.l.loadFailed.Add(1)
	time.AfterFunc(millis(f.GetRetryAfterMs()), func() { _ = conn.LoadRooms() })
}

func sleep(ctx context.Context, conn *wstest.Conn, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	var done <-chan struct{}
	if conn != nil {
		done = conn.Done()
	}
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	case <-done:
		return false
	}
}

func closedOr(ctx context.Context, conn *wstest.Conn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return conn.Err()
}

func millis(ms uint32) time.Duration { return time.Duration(ms) * time.Millisecond }

type dropClock struct {
	drops, heals                  atomic.Int64
	firstDrop, lastDrop, lastHeal atomic.Int64
}

func (d *dropClock) dropped() {
	now := time.Now().UnixNano()
	d.firstDrop.CompareAndSwap(0, now)
	d.lastDrop.Store(now)
	d.drops.Add(1)
}

func (d *dropClock) recovered() {
	d.lastHeal.Store(time.Now().UnixNano())
	d.heals.Add(1)
}

func (d *dropClock) pending() int64 { return d.drops.Load() - d.heals.Load() }
