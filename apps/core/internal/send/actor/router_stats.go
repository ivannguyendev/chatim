package actor

type Stats struct {
	Yields       uint64
	CIDElsewhere uint64
}

func (r *Router) Stats() Stats {
	return Stats{Yields: r.yields.Load(), CIDElsewhere: r.cidElsewhere.Load()}
}
