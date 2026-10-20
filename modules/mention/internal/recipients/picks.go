package recipients

import "github.com/ivannguyendev/chatim/modules/mention/internal/domain"

type picks struct {
	sender string
	chosen map[string]domain.Target
}

func (p picks) offer(user string, t domain.Target) {
	if user == p.sender {
		return
	}
	if cur, ok := p.chosen[user]; ok && !before(t, cur) {
		return
	}
	p.chosen[user] = t
}

func before(a, b domain.Target) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.ID < b.ID
}

func split(targets []domain.Target) (users, groups map[string]domain.Target, everyone *domain.Target) {
	users, groups = make(map[string]domain.Target), make(map[string]domain.Target)
	for _, t := range targets {
		switch t.Kind {
		case domain.TargetUser:
			users[t.ID] = t
		case domain.TargetGroup:
			groups[t.ID] = t
		case domain.TargetAll:
			everyone = &t
		}
	}
	return users, groups, everyone
}

func active(members []domain.Member) []domain.Member {
	out := make([]domain.Member, 0, len(members))
	for _, m := range members {
		if m.Active {
			out = append(out, m)
		}
	}
	return out
}
