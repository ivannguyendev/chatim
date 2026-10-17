package consumer

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

const progressEvery = AckWait / 3

type tracked struct {
	jsconsume.Delivery
	mu      sync.Mutex
	settled bool
}

func track(ds []jsconsume.Delivery) []*tracked {
	out := make([]*tracked, len(ds))
	for i, d := range ds {
		out[i] = &tracked{Delivery: d}
	}
	return out
}

func (t *tracked) Ack() error { return t.finish(t.Delivery.Ack) }

func (t *tracked) Term() error { return t.finish(t.Delivery.Term) }

func (t *tracked) Nak(delay time.Duration) error {
	return t.finish(func() error { return t.Delivery.Nak(delay) })
}

func (t *tracked) InProgress() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.settled {
		return nil
	}
	return t.Delivery.InProgress()
}

func (t *tracked) finish(settle func() error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.settled = true
	return settle()
}

func (c *Consumer) keepAlive(ctx context.Context, batch []*tracked) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(progressEvery)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				for _, d := range batch {
					if err := d.InProgress(); err != nil {
						c.fails.Warn(ctx, progressMsg, "err", err)
					}
				}
			}
		}
	})
	return func() {
		close(done)
		wg.Wait()
	}
}
