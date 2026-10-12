package actor

func (a *actor) contend(e *entry) {
	if !a.contended {
		a.contended = true
		a.r.yield(a)
	}
	a.fail(e, errSeqContention, false)
}

func (r *Router) yield(a *actor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a.requestRetire() {
		r.yields.Add(1)
	}
}
