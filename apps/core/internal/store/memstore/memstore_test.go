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
	storetest.RunFeed(t, func(*testing.T) (store.Messages, store.ChangeFeed) {
		msgs := memstore.NewMessages()
		return msgs, memstore.NewFeed(msgs)
	})
}
