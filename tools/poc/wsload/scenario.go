package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/poc/internal/openloop"
	"github.com/ivannguyendev/chatim/tools/poc/internal/wsload"
)

const (
	sendTimeout   = 10 * time.Second
	maxInflight   = 4096
	deliveryGrace = 5 * time.Second
	killQuiet     = 5 * time.Second
	killPoll      = 100 * time.Millisecond
	killSendSpan  = time.Hour
)

func (l *load) runScenario(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	paced := l.cfg.connectRate
	if l.cfg.scenario == scenarioBurst {
		paced = 0
	}
	l.connectAll(ctx, &wg, paced)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch l.cfg.scenario {
	case scenarioSteady:
		l.send(ctx, openloop.Plan{Rate: l.cfg.rate, Duration: l.cfg.duration, MaxInflight: maxInflight})
		return nil
	case scenarioKill:
		return l.kill(ctx)
	}
	return nil
}

func (l *load) connectAll(ctx context.Context, wg *sync.WaitGroup, rate int) {
	start := time.Now()
	for i, c := range l.clients {
		if rate > 0 {
			due := start.Add(time.Duration(int64(i) * int64(time.Second) / int64(rate)))
			if !sleep(ctx, nil, time.Until(due)) {
				break
			}
		}
		wg.Go(func() { c.run(ctx) })
	}
	for _, c := range l.clients {
		select {
		case <-c.first:
		case <-ctx.Done():
			return
		}
	}
	r := &l.report.Connect
	r.Ready, r.Failed = l.ready.Load(), l.failed.Load()
	r.ElapsedSeconds = seconds(time.Since(start))
	if last := l.lastReady.Load(); last > 0 {
		r.LastReadySeconds = seconds(time.Unix(0, last).Sub(start))
	}
	r.Time = latencyOf(l.connect.Summary())
	r.RetryLater = l.retryLater.Load()
	fmt.Printf("connect: ready %d failed %d retry_later %d, last ready after %.2fs, connect time %v\n",
		r.Ready, r.Failed, r.RetryLater, r.LastReadySeconds, l.connect.Summary())
}

func (l *load) loggedIn(started time.Time, err error) {
	if err != nil {
		l.failed.Add(1)
		l.errs.add("login", err)
		return
	}
	now := time.Now()
	l.connect.Add(now.Sub(started))
	l.ready.Add(1)
	for {
		old := l.lastReady.Load()
		if now.UnixNano() <= old || l.lastReady.CompareAndSwap(old, now.UnixNano()) {
			return
		}
	}
}

func (l *load) send(ctx context.Context, plan openloop.Plan) {
	counts := openloop.Run(ctx, plan, l.fire)
	if !sleep(ctx, nil, deliveryGrace) {
		return
	}
	l.report.Send = l.sendReport(counts)
}

func (l *load) fire(ctx context.Context, s openloop.Shot) {
	group := s.Index % l.layout.Groups
	conn := l.clients[l.layout.SenderOf(group)].conn.Load()
	if conn == nil {
		l.skipped.Add(1)
		return
	}
	req := &chatimv1.SendMessageRequest{RoomId: l.rooms[group], Cid: openloop.CID(l.run, s.Index), Text: wsload.StampText(l.run, time.Now())}
	callCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	_, err := conn.Call(callCtx, &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_SendMessage{SendMessage: req}})
	cancel()
	l.sends.Record(openloop.Result{Shot: s, Done: time.Now(), Code: codeOf(err), Attempts: 1})
	if err == nil {
		l.expected.Add(int64(l.layout.MembersOf(group)))
	} else {
		l.errs.add("send", err)
	}
}

func (l *load) kill(ctx context.Context) error {
	sendCtx, stopSend := context.WithCancel(ctx)
	defer stopSend()
	sent := make(chan openloop.Counts, 1)
	go func() {
		sent <- openloop.Run(sendCtx, openloop.Plan{Rate: l.cfg.rate, Duration: killSendSpan, MaxInflight: maxInflight}, l.fire)
	}()
	retryBefore := l.retryLater.Load()
	fmt.Println("kill-ready")
	_ = os.Stdout.Sync()
	err := l.awaitRecovery(ctx)
	k := &killReport{}
	l.report.Kill = k
	k.Dropped, k.Reconnected, k.NotReconnected = l.drops.drops.Load(), l.drops.heals.Load(), l.drops.pending()
	k.RetryLater = l.retryLater.Load() - retryBefore
	if first, healed := l.drops.firstDrop.Load(), l.drops.lastHeal.Load(); first > 0 && healed > first {
		k.RecoverySeconds = seconds(time.Duration(healed - first))
	}
	fmt.Printf("kill: dropped %d reconnected %d not reconnected %d retry_later %d recovery %.2fs\n",
		k.Dropped, k.Reconnected, k.NotReconnected, k.RetryLater, k.RecoverySeconds)
	stopSend()
	counts := <-sent
	if sleep(ctx, nil, deliveryGrace) {
		l.report.Send = l.sendReport(counts)
	}
	return err
}

func (l *load) awaitRecovery(ctx context.Context) error {
	deadline := time.Now().Add(l.cfg.duration)
	for l.drops.firstDrop.Load() == 0 {
		if time.Now().After(deadline) {
			return fmt.Errorf("no connection dropped within %v of kill-ready", l.cfg.duration)
		}
		if !sleep(ctx, nil, killPoll) {
			return ctx.Err()
		}
	}
	deadline = time.Unix(0, l.drops.firstDrop.Load()).Add(l.cfg.duration)
	for {
		quiet := time.Since(time.Unix(0, l.drops.lastDrop.Load())) >= killQuiet
		if l.drops.pending() == 0 && quiet {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d connections not ready again within %v of the first drop", l.drops.pending(), l.cfg.duration)
		}
		if !sleep(ctx, nil, killPoll) {
			return ctx.Err()
		}
	}
}

func codeOf(err error) codes.Code {
	if s, ok := status.FromError(err); ok {
		return s.Code()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return codes.DeadlineExceeded
	}
	return codes.Unavailable
}
