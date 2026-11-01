package authz

type Grant struct {
	tenantAdmin bool
	perms       map[Permission]struct{}
}

func NewGrant(tenantAdmin bool, perms []Permission) Grant {
	set := make(map[Permission]struct{}, len(perms))
	for _, p := range perms {
		set[p] = struct{}{}
	}
	return Grant{tenantAdmin: tenantAdmin, perms: set}
}

func (g Grant) TenantAdmin() bool {
	return g.tenantAdmin
}

func (g Grant) Has(p Permission) bool {
	if g.tenantAdmin {
		return true
	}
	_, ok := g.perms[p]
	return ok
}

func (g Grant) Covers(other Grant) bool {
	if g.tenantAdmin {
		return true
	}
	if other.tenantAdmin {
		return false
	}
	for p := range other.perms {
		if _, ok := g.perms[p]; !ok {
			return false
		}
	}
	return true
}

func (g Grant) CoversAll(perms []Permission) bool {
	for _, p := range perms {
		if !g.Has(p) {
			return false
		}
	}
	return true
}
