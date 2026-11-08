package consumer

import "context"

func (c *Consumer) enqueue(ctx context.Context, j *job) {
	key := j.ev.Ref.Key()
	c.mu.Lock()
	q, busy := c.queues[key]
	c.queues[key] = append(q, j)
	c.mu.Unlock()
	if !busy {
		c.running.Go(func() { c.runChain(ctx, key) })
	}
}

func (c *Consumer) runChain(ctx context.Context, key string) {
	for j := c.pop(key); j != nil; j = c.pop(key) {
		c.run(ctx, j)
	}
}

func (c *Consumer) pop(key string) *job {
	c.mu.Lock()
	defer c.mu.Unlock()
	q := c.queues[key]
	if len(q) == 0 {
		delete(c.queues, key)
		return nil
	}
	j := q[0]
	q[0] = nil
	c.queues[key] = q[1:]
	return j
}

func (c *Consumer) run(ctx context.Context, j *job) {
	defer c.release()
	if c.stopped.Err() != nil || ctx.Err() != nil {
		c.report(ctx, j.d.Nak(0))
		return
	}
	c.settle(ctx, j.d, c.process(ctx, j))
}
