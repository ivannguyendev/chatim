package mongostore

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

func TestMongoEventStatesContract(t *testing.T) {
	client := itClient(t)
	storetest.RunEventStates(t, func(t *testing.T) storetest.EventStateStores {
		s, db := itStore(t, client)
		return storetest.EventStateStores{States: s.EventStates(), Checkpoints: s.SweepCheckpoints(), Seeder: stateSeeder{db: db}}
	})
}

func TestMongoFactStatesContract(t *testing.T) {
	client := itClient(t)
	storetest.RunFactStates(t, func(t *testing.T) storetest.FactStateStores {
		s, _ := itStore(t, client)
		return storetest.FactStateStores{Edits: s, Interactions: s.Interactions(), Pins: s.Pins(), Hidden: s.Hidden(), States: s.EventStates()}
	})
}

func TestMongoMemberStatesContract(t *testing.T) {
	client := itClient(t)
	storetest.RunMemberStates(t, func(t *testing.T) storetest.MemberStateStores {
		s, _ := itStore(t, client)
		return storetest.MemberStateStores{Rooms: s, States: s.EventStates()}
	})
}
