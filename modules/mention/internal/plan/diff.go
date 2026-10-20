package plan

import "github.com/ivannguyendev/chatim/modules/mention/internal/domain"

func Diff(current, previous map[string]domain.Target) (notify, retract map[string]domain.Target) {
	return missingFrom(current, previous), missingFrom(previous, current)
}

func missingFrom(from, other map[string]domain.Target) map[string]domain.Target {
	out := make(map[string]domain.Target)
	for user, t := range from {
		if _, ok := other[user]; !ok {
			out[user] = t
		}
	}
	return out
}
