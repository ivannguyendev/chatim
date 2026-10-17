package sweep_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestConfigDefaultsAndLimits(t *testing.T) {
	if err := (sweep.Config{SubjectRoot: "evt"}).Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	bad := []sweep.Config{
		{},
		{SubjectRoot: "evt", Interval: -time.Second},
		{SubjectRoot: "evt", MinAge: -time.Second},
		{SubjectRoot: "evt", Lookback: -time.Second},
		{SubjectRoot: "evt", Tick: -time.Second},
		{SubjectRoot: "evt", AckTimeout: -time.Second},
		{SubjectRoot: "evt", Rate: -1},
		{SubjectRoot: "evt", Batch: -1},
		{SubjectRoot: "evt", Batch: store.MaxPendingPage + 1},
	}
	for _, c := range bad {
		if err := c.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalidArgument", c, err)
		}
	}
}

func TestNewNeedsEveryDependency(t *testing.T) {
	states := memstore.NewEventStates(memstore.EventStateStores{})
	full := sweep.Deps{States: states, Checkpoints: memstore.NewSweepCheckpoints(), Owner: &owner{}, JS: &publishtest.JetStream{}}
	broken := []func(*sweep.Deps){
		func(d *sweep.Deps) { d.States = nil },
		func(d *sweep.Deps) { d.Checkpoints = nil },
		func(d *sweep.Deps) { d.Owner = nil },
		func(d *sweep.Deps) { d.JS = nil },
		func(d *sweep.Deps) {
			d.Rebuilders = map[store.StateCollection]sweep.Rebuilder{store.StateMessages: nil}
		},
		func(d *sweep.Deps) {
			d.Rebuilders = map[store.StateCollection]sweep.Rebuilder{0: sweep.NewRoomRebuilder(memstore.NewRooms(), nil)}
		},
	}
	for i, breakIt := range broken {
		d := full
		breakIt(&d)
		if _, err := sweep.New(d, setup, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("case %d: New = %v, want ErrInvalidArgument", i, err)
		}
	}
	if _, err := sweep.New(full, sweep.Config{}, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("New without a subject root = %v, want ErrInvalidArgument", err)
	}
	if _, err := sweep.New(full, setup, nil); err != nil {
		t.Errorf("New = %v", err)
	}
}
