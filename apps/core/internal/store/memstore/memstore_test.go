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
