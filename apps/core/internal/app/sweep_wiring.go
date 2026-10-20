package app

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/interaction/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

const sweepRoomTypes = 65536

func wireSweeper(cfg config.Config, cl *clients, st *mongostore.Store, owner sweep.Owner, log *slog.Logger) (*sweep.Sweeper, error) {
	projector, err := pinproj.New(st.Pins(), st)
	if err != nil {
		return nil, fmt.Errorf("wire sweeper pin projector: %w", err)
	}
	stores := sweep.RebuilderStores{
		Messages: st, Rooms: st, Edits: st, Interactions: st.Interactions(),
		Pins: st.Pins(), PinProjector: projector, Members: st, Hidden: st.Hidden(),
	}
	rebuilders, err := sweep.NewRebuilders(stores, eventkit.NewRoomTypes(st, sweepRoomTypes), time.Now)
	if err != nil {
		return nil, fmt.Errorf("wire sweep rebuilders: %w", err)
	}
	s, err := sweep.New(sweep.Deps{
		States: st.EventStates(), Checkpoints: st.SweepCheckpoints(), Owner: owner, JS: cl.sweepJS, Rebuilders: rebuilders,
	}, cfg.Sweep, log)
	if err != nil {
		return nil, fmt.Errorf("wire event sweeper: %w", err)
	}
	return s, nil
}
