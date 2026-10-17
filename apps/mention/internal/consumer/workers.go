package consumer

import (
	"context"
	"sync"
)

func (c *Consumer) work(ctx context.Context, jobs []job) {
	if len(jobs) == 0 {
		return
	}
	queue := make(chan *job)
	var wg sync.WaitGroup
	for range min(c.cfg.Workers, len(jobs)) {
		wg.Go(func() {
			for j := range queue {
				c.settle(ctx, j.d, c.process(ctx, j))
			}
		})
	}
	sent := c.feed(ctx, queue, jobs)
	close(queue)
	wg.Wait()
	for _, j := range jobs[sent:] {
		c.report(ctx, j.d.Nak(0))
	}
}

func (c *Consumer) feed(ctx context.Context, queue chan<- *job, jobs []job) int {
	for i := range jobs {
		if !c.live(ctx) {
			return i
		}
		select {
		case queue <- &jobs[i]:
		case <-c.stopped.Done():
			return i
		case <-ctx.Done():
			return i
		}
	}
	return len(jobs)
}
