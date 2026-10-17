package config_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func TestDefaultStopPlanFitsTheBudget(t *testing.T) {
	setEnv(t, nil)
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	plan := c.StopPlan()
	want := config.StopPlan{
		DrainDelay: 2 * time.Second, GRPC: 5 * time.Second, Sweeper: time.Second, Projections: 4 * time.Second,
		Timer: 1500 * time.Millisecond, Router: 3 * time.Second, CIDBatch: 200 * time.Millisecond, Flusher: time.Second,
		Publisher: 5 * time.Second, StateClear: time.Second, Slots: 5 * time.Second, Close: time.Second,
	}
	if plan != want {
		t.Fatalf("StopPlan() = %+v, want %+v", plan, want)
	}
	if plan.Total() != 29700*time.Millisecond || c.ShutdownBudget != 30*time.Second {
		t.Errorf("plan %v of budget %v, want 29.7s of 30s", plan.Total(), c.ShutdownBudget)
	}
}

func TestProjectionStopPhaseFollowsTheFetchWait(t *testing.T) {
	setEnv(t, map[string]string{"PROJECTION_FETCH_WAIT": "500ms"})
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.StopPlan(); got.Projections != 1500*time.Millisecond || got.Total() != 27200*time.Millisecond {
		t.Errorf("projections phase %v plan %v, want 1.5s and 27.2s", got.Projections, got.Total())
	}
}
