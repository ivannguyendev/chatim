package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/platform/metrics"
)

func TestHandlerExposesCountersAndGaugesFromSources(t *testing.T) {
	queueFull, refused := 3.0, 1.0
	h, err := metrics.Handler([]metrics.Source{
		{Name: "publish_dropped_total", Help: "Events dropped before JetStream acked them.", Labels: map[string]string{"reason": "queue_full"}, Read: func() float64 { return queueFull }},
		{Name: "publish_dropped_total", Help: "Events dropped before JetStream acked them.", Labels: map[string]string{"reason": "refused"}, Read: func() float64 { return refused }},
		{Name: "reconcile_running", Help: "1 while this core runs a reconcile term.", Gauge: true, Read: func() float64 { return 1 }},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`chatim_core_publish_dropped_total{reason="queue_full"} 3`,
		`chatim_core_publish_dropped_total{reason="refused"} 1`,
		`# TYPE chatim_core_reconcile_running gauge`,
		`chatim_core_reconcile_running 1`,
		`go_goroutines`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
}

func TestHandlerRejectsConflictingSources(t *testing.T) {
	_, err := metrics.Handler([]metrics.Source{
		{Name: "x_total", Help: "a", Read: func() float64 { return 0 }},
		{Name: "x_total", Help: "a", Read: func() float64 { return 0 }},
	})
	if err == nil {
		t.Fatal("Handler accepted two identical sources")
	}
}
