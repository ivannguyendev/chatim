package memstore_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

func TestMentionsContract(t *testing.T) {
	storetest.RunMentions(t, func(*testing.T) store.Mentions { return memstore.NewMentions() })
}
