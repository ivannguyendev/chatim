package projection_test

import (
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
)

func TestConsumerRunsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		synctest.Wait()
		if err := rg.c.Run(t.Context()); err == nil {
			t.Errorf("second Run = nil, want an error")
		}
		rg.stop(t)
	})
}

func TestNewConsumerRefusesMissingPartsAndBadConfig(t *testing.T) {
	p, src := projection.NewRoomActivity(&fakeWriter{}), newFakeSource()
	for name, build := range map[string]func() (*projection.Consumer, error){
		"no projection": func() (*projection.Consumer, error) { return projection.NewConsumer(nil, src, evtConfig, nil) },
		"no source":     func() (*projection.Consumer, error) { return projection.NewConsumer(p, nil, evtConfig, nil) },
		"bad config": func() (*projection.Consumer, error) {
			return projection.NewConsumer(p, src, projection.Config{Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchBatch: projection.MaxFetchBatch + 1}, nil)
		},
	} {
		if _, err := build(); err == nil {
			t.Errorf("%s: NewConsumer = nil error", name)
		}
	}
	if c, err := projection.NewConsumer(p, src, evtConfig, nil); err != nil || c.Name() != projection.RoomActivityName {
		t.Errorf("NewConsumer = %v; want a consumer named %s", err, projection.RoomActivityName)
	}
}
