package projection_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
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
	hs, src := []projection.Projection{projection.NewRoomActivity(&fakeWriter{})}, newFakeSource()
	for name, build := range map[string]func() (*projection.Consumer, error){
		"no handlers": func() (*projection.Consumer, error) { return projection.NewConsumer(nil, src, evtConfig, nil) },
		"nil handler": func() (*projection.Consumer, error) {
			return projection.NewConsumer([]projection.Projection{nil}, src, evtConfig, nil)
		},
		"no source": func() (*projection.Consumer, error) { return projection.NewConsumer(hs, nil, evtConfig, nil) },
		"bad config": func() (*projection.Consumer, error) {
			return projection.NewConsumer(hs, src, projection.Config{Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchBatch: projection.MaxFetchBatch + 1}, nil)
		},
	} {
		if _, err := build(); err == nil {
			t.Errorf("%s: NewConsumer = nil error", name)
		}
	}
	if c, err := projection.NewConsumer(hs, src, evtConfig, nil); err != nil || c.Name() != projection.DurableName {
		t.Errorf("NewConsumer = %v; want a consumer named %s", err, projection.DurableName)
	}
}

func TestConsumerFetchesWithItsSettings(t *testing.T) {
	tuned := projection.Config{Stream: "CHATIM_EVT", SubjectRoot: subjectRoot, FetchBatch: 128, FetchWait: 250 * time.Millisecond}
	for name, tc := range map[string]struct {
		cfg  projection.Config
		want fetchAsk
	}{
		"default":    {evtConfig, fetchAsk{1024, time.Second}},
		"configured": {tuned, fetchAsk{128, 250 * time.Millisecond}},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				src := newFakeSource()
				hs := []projection.Projection{projection.NewRoomActivity(&fakeWriter{})}
				c := built(projection.NewConsumer(hs, src, tc.cfg, (&testlog.Sink{}).Logger()))(t)
				done := make(chan error, 1)
				go func() { done <- c.Run(t.Context()) }()
				synctest.Wait()
				if got, ok := src.firstAsk(); !ok || got != tc.want || src.floorReads() != 1 {
					t.Errorf("first fetch %+v (asked %v) after %d floor reads, want %+v after one", got, ok, src.floorReads(), tc.want)
				}
				if err := c.Close(t.Context()); err != nil {
					t.Fatalf("Close: %v", err)
				}
				<-done
			})
		})
	}
}

func TestConsumerRetriesTheAckFloorBeforeItFetches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := &consumerRig{sink: &testlog.Sink{}, src: newFakeSource(), writer: &fakeWriter{}, result: make(chan error, 1)}
		rg.src.setFloor(0, errBoom)
		rg.c = built(projection.NewConsumer([]projection.Projection{projection.NewRoomActivity(rg.writer)}, rg.src, evtConfig, rg.sink.Logger()))(t)
		go func() { rg.result <- rg.c.Run(t.Context()) }()
		synctest.Wait()
		if rg.src.asks() != 0 || rg.sink.Count("projection could not read its ack floor; retrying") != 1 {
			t.Fatalf("fetched %d times with no ack floor, want none and one warning", rg.src.asks())
		}
		rg.src.setFloor(0, nil)
		time.Sleep(projection.DefaultFetchWait)
		synctest.Wait()
		if rg.src.asks() != 1 || rg.src.floorReads() != 2 {
			t.Fatalf("after the floor came back: %d fetches, %d floor reads; want 1 and 2", rg.src.asks(), rg.src.floorReads())
		}
		rg.stop(t)
	})
}
