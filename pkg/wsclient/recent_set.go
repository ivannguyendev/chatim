package wsclient

type recentSet[K comparable] struct {
	ring    []K
	next    int
	members map[K]struct{}
}

func newRecentSet[K comparable](size int) *recentSet[K] {
	return &recentSet[K]{ring: make([]K, 0, size), members: make(map[K]struct{}, size)}
}

func (s *recentSet[K]) add(key K) bool {
	if _, ok := s.members[key]; ok {
		return false
	}
	if len(s.ring) < cap(s.ring) {
		s.ring = append(s.ring, key)
	} else {
		delete(s.members, s.ring[s.next])
		s.ring[s.next] = key
		s.next = (s.next + 1) % len(s.ring)
	}
	s.members[key] = struct{}{}
	return true
}
