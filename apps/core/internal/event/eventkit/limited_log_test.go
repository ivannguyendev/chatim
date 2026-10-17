package eventkit_test

import (
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestLimitedLogWarnsAtMostOncePerSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var sink testlog.Sink
		l := eventkit.NewLimitedLog(sink.Logger())
		l.Warn(t.Context(), "boom")
		l.Warn(t.Context(), "boom")
		time.Sleep(999 * time.Millisecond)
		l.Warn(t.Context(), "boom")
		if got := sink.Count("boom"); got != 1 {
			t.Fatalf("warnings within one second = %d, want 1", got)
		}
		time.Sleep(time.Millisecond)
		l.Warn(t.Context(), "boom")
		if got := sink.Count("boom"); got != 2 {
			t.Fatalf("warnings after one second = %d, want 2", got)
		}
	})
}
