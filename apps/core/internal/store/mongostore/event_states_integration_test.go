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
