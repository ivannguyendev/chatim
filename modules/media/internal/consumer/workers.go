package consumer

import (
	"context"
	"sync"
)

func chainsOf(jobs []job) [][]*job {
	at := make(map[string]int, len(jobs))
	var chains [][]*job
	for i := range jobs {
		key := jobs[i].ev.Ref.Key()
		n, seen := at[key]
		if !seen {
			n = len(chains)
			at[key] = n
			chains = append(chains, nil)
		}
		chains[n] = append(chains[n], &jobs[i])
	}
	return chains
}

func (c *Consumer) work(ctx context.Context, jobs []job) {
	chains := chainsOf(jobs)
	if len(chains) == 0 {
		return
	}
	queue := make(chan []*job)
	var wg sync.WaitGroup
	for range min(c.cfg.Workers, len(chains)) {
		wg.Go(func() {
			for chain := range queue {
				c.runChain(ctx, chain)
			}
		})
	}
	sent := c.feed(ctx, queue, chains)
	close(queue)
	wg.Wait()
	for _, chain := range chains[sent:] {
		for _, j := range chain {
			c.report(ctx, j.d.Nak(0))
		}
	}
}

func (c *Consumer) runChain(ctx context.Context, chain []*job) {
	for _, j := range chain {
		if !c.live(ctx) {
			c.report(ctx, j.d.Nak(0))
			continue
		}
		c.settle(ctx, j.d, c.process(ctx, j))
	}
}

func (c *Consumer) feed(ctx context.Context, queue chan<- []*job, chains [][]*job) int {
	for i, chain := range chains {
		if !c.live(ctx) {
			return i
		}
		select {
		case queue <- chain:
		case <-c.stopped.Done():
			return i
		case <-ctx.Done():
			return i
		}
	}
	return len(chains)
}
