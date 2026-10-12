package mongostore

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

func TestMongoMessageCountsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunMessageCounts(t, func(t *testing.T) storetest.CountableMessages {
		s, _ := itStore(t, client)
		return s
	})
}
