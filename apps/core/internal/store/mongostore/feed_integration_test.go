package mongostore

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

func TestMongoFeedContract(t *testing.T) {
	client := itClient(t)
	storetest.RunFeed(t, func(t *testing.T) (store.Messages, store.Rooms, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s, s, NewFeed(db)
	})
}

func TestMongoEditFeedContract(t *testing.T) {
	client := itClient(t)
	storetest.RunEditFeed(t, func(t *testing.T) (store.Edits, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s, NewFeed(db)
	})
}

func TestMongoReactionFeedContract(t *testing.T) {
	client := itClient(t)
	storetest.RunReactionFeed(t, func(t *testing.T) (store.Reactions, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s.Reactions(), NewFeed(db)
	})
}

func TestMongoPinFeedContract(t *testing.T) {
	client := itClient(t)
	storetest.RunPinFeed(t, func(t *testing.T) (store.Pins, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s.Pins(), NewFeed(db)
	})
}
