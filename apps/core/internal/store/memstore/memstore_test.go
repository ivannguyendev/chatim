package memstore_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

func TestContract(t *testing.T) {
	storetest.Run(t, func(*testing.T) (store.Messages, store.Rooms) {
		return memstore.NewMessages(), memstore.NewRooms()
	})
}

func TestFeedContract(t *testing.T) {
	storetest.RunFeed(t, func(*testing.T) (store.Messages, store.Rooms, store.ChangeFeed) {
		msgs, rooms := memstore.NewMessages(), memstore.NewRooms()
		return msgs, rooms, memstore.NewFeed(msgs, rooms, nil)
	})
}

func TestEditsContract(t *testing.T) {
	storetest.RunEdits(t, func(*testing.T) (storetest.EditableMessages, storetest.ClearableRooms, store.Edits, store.Hidden) {
		return memstore.NewMessages(), memstore.NewRooms(), memstore.NewEdits(), memstore.NewHidden()
	})
}

func TestEditFeedContract(t *testing.T) {
	storetest.RunEditFeed(t, func(*testing.T) (store.Edits, store.ChangeFeed) {
		msgs, edits := memstore.NewMessages(), memstore.NewEdits()
		return edits, memstore.NewFeed(msgs, nil, edits)
	})
}

func TestInteractionsContract(t *testing.T) {
	storetest.RunInteractions(t, func(*testing.T) (storetest.ReactableMessages, store.Interactions) {
		return memstore.NewMessages(), memstore.NewInteractions()
	})
}

func TestPinsContract(t *testing.T) {
	storetest.RunPins(t, func(*testing.T) (storetest.PinnableRooms, store.Pins) {
		return memstore.NewRooms(), memstore.NewPins()
	})
}

func TestReactionFeedContract(t *testing.T) {
	storetest.RunInteractionFeed(t, func(*testing.T) (store.Interactions, store.ChangeFeed) {
		interactions := memstore.NewInteractions()
		return interactions, memstore.NewFeed(memstore.NewMessages(), nil, nil, memstore.WithInteractions(interactions))
	})
}

func TestPinFeedContract(t *testing.T) {
	storetest.RunPinFeed(t, func(*testing.T) (store.Pins, store.ChangeFeed) {
		pins := memstore.NewPins()
		return pins, memstore.NewFeed(memstore.NewMessages(), nil, nil, memstore.WithPins(pins))
	})
}

func TestMembersContract(t *testing.T) {
	storetest.RunMembers(t, func(*testing.T) storetest.MemberRooms { return memstore.NewRooms() })
}

func TestMemberFeedContract(t *testing.T) {
	storetest.RunMemberFeed(t, func(*testing.T) (storetest.MemberRooms, store.Hidden, store.ChangeFeed) {
		rooms, hidden := memstore.NewRooms(), memstore.NewHidden()
		return rooms, hidden, memstore.NewFeed(memstore.NewMessages(), rooms, nil, memstore.WithHidden(hidden))
	})
}
