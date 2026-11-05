package mongostore

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/chat/internal/store/storetest"
)

func TestMongoMessageCountsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunMessageCounts(t, func(t *testing.T) storetest.CountableMessages {
		s, _ := itStore(t, client)
		return s
	})
}

func TestMongoMessageStatesContract(t *testing.T) {
	client := itClient(t)
	storetest.RunMessageStates(t, func(t *testing.T) storetest.MessageStateStores {
		s, _ := itStore(t, client)
		return storetest.MessageStateStores{Messages: s, States: s.EventStates()}
	})
}
