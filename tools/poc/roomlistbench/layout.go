package main

import "fmt"

type benchUser struct {
	name  string
	size  int
	rooms []string
}

type roomSpec struct {
	creator  string
	members  []string
	messages int
	private  *benchUser
}

type layout struct {
	users      []*benchUser
	background []roomSpec
	private    []roomSpec
}

func planLayout(c config, run string) layout {
	var l layout
	maxBackground := 0
	for _, size := range c.sizes {
		for k := range c.perSize {
			l.users = append(l.users, &benchUser{name: fmt.Sprintf("%s-s%d-u%d", run, size, k), size: size})
		}
		maxBackground = max(maxBackground, size-c.private)
	}
	owner := run + "-owner"
	for i := range maxBackground {
		spec := roomSpec{creator: owner, members: []string{owner}, messages: c.backgroundMessages}
		for _, u := range l.users {
			if u.size-c.private > i {
				spec.members = append(spec.members, u.name)
			}
		}
		l.background = append(l.background, spec)
	}
	for ui, u := range l.users {
		for j := range c.private {
			peer := fmt.Sprintf("%s-p%d-%d", run, ui, j)
			l.private = append(l.private, roomSpec{creator: peer, members: []string{peer, u.name}, messages: c.privateMessages, private: u})
		}
	}
	return l
}

func (l layout) rooms() []roomSpec {
	all := make([]roomSpec, 0, len(l.background)+len(l.private))
	all = append(all, l.background...)
	return append(all, l.private...)
}
