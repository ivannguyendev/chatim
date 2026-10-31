package iamsync_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/iamsync"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

func metricsText(t *testing.T, s *iamsync.Syncer) string {
	t.Helper()
	h, err := metrics.Handler("test", s.Sources())
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	return string(body)
}

func TestAgeCountsFromStartBeforeTheFirstSync(t *testing.T) {
	r := newRig(t, testConfig(), &fakeDoer{}, nil)
	time.Sleep(10 * time.Millisecond)
	body := metricsText(t, r.syncer)
	if !strings.Contains(body, `test_iam_refused{kind="revocations"} 0`) || strings.Contains(body, "test_iam_revocations_age_seconds 0\n") {
		t.Fatalf("metrics = %s", body)
	}
}
