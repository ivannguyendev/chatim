package memstore_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store/storetest"
)

func TestMentionsContract(t *testing.T) {
	storetest.RunMentions(t, func(*testing.T) store.Mentions { return memstore.NewMentions() })
}

func TestRoomsContract(t *testing.T) {
	storetest.RunRooms(t, func(*testing.T) store.Rooms { return memstore.NewRooms() })
}

func TestUserGroupsContract(t *testing.T) {
	storetest.RunUserGroups(t, func(*testing.T) store.UserGroups { return memstore.NewUserGroups() })
}

func TestGroupsContract(t *testing.T) {
	storetest.RunGroups(t, func(*testing.T) store.Groups { return memstore.NewGroups() })
}
