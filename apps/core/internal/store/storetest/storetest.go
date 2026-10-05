package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type messagesCase struct {
	name string
	run  func(t *testing.T, s store.Messages)
}

type roomsCase struct {
	name string
	run  func(t *testing.T, s store.Rooms)
}

func Run(t *testing.T, open func(t *testing.T) (store.Messages, store.Rooms)) {
	t.Helper()
	groups := []struct {
		name  string
		cases []messagesCase
	}{
		{"Insert", insertCases()},
		{"Last", lastCases()},
		{"Page", pageCases()},
		{"Walk", walkCases()},
		{"Find", findCases()},
	}
	for _, g := range groups {
		t.Run(g.name, func(t *testing.T) {
			for _, c := range g.cases {
				t.Run(c.name, func(t *testing.T) {
					msgs, _ := open(t)
					c.run(t, msgs)
				})
			}
		})
	}
	t.Run("Rooms", func(t *testing.T) {
		for _, c := range append(roomsCases(), activityCases()...) {
			t.Run(c.name, func(t *testing.T) {
				_, rooms := open(t)
				c.run(t, rooms)
			})
		}
	})
}
