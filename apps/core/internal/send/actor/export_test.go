package actor

func (r *Router) ActorCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.actors)
}

const MemberCacheTTL = memberCacheTTL
