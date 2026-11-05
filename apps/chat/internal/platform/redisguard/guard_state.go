package redisguard

func (g *Guard) Degraded() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.degraded
}
