package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/core/metrics"
)

func TestHandlerExposesCountersAndGaugesFromSources(t *testing.T) {
	queueFull, refused := 3.0, 1.0
	h, err := metrics.Handler("acme", "orders", []metrics.Source{
		{Name: "dropped_total", Help: "Orders dropped before the store acked them.", Labels: map[string]string{"reason": "queue_full"}, Read: func() float64 { return queueFull }},
		{Name: "dropped_total", Help: "Orders dropped before the store acked them.", Labels: map[string]string{"reason": "refused"}, Read: func() float64 { return refused }},
		{Name: "sweep_running", Help: "1 while this node runs the sweeper.", Gauge: true, Read: func() float64 { return 1 }},
		{Subsystem: "billing", Name: "lag_seconds", Help: "Billing lag.", Gauge: true, Read: func() float64 { return 2 }},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`acme_orders_dropped_total{reason="queue_full"} 3`,
		`acme_orders_dropped_total{reason="refused"} 1`,
		`# TYPE acme_orders_sweep_running gauge`,
		`acme_orders_sweep_running 1`,
		`acme_billing_lag_seconds 2`,
		`go_goroutines`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
}

func TestHandlerRejectsConflictingSources(t *testing.T) {
	_, err := metrics.Handler("acme", "orders", []metrics.Source{
		{Name: "x_total", Help: "a", Read: func() float64 { return 0 }},
		{Name: "x_total", Help: "a", Read: func() float64 { return 0 }},
	})
	if err == nil {
		t.Fatal("Handler accepted two identical sources")
	}
}

func TestHandlerRejectsAnEmptyNamespace(t *testing.T) {
	_, err := metrics.Handler("", "orders", []metrics.Source{
		{Name: "x_total", Help: "a", Read: func() float64 { return 0 }},
	})
	if err == nil {
		t.Fatal("Handler accepted an empty namespace")
	}
}

func TestHandlerNamesSourcesAfterTheGivenNamespaceAndSubsystem(t *testing.T) {
	h, err := metrics.Handler("shop", "carts", []metrics.Source{
		{Name: "open", Help: "Open carts.", Gauge: true, Read: func() float64 { return 7 }},
		{Subsystem: "billing", Name: "lag_seconds", Help: "Billing lag.", Gauge: true, Read: func() float64 { return 2 }},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{`shop_carts_open 7`, `shop_billing_lag_seconds 2`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
	if strings.Contains(string(body), "acme_") || strings.Contains(string(body), "shop_orders_") {
		t.Errorf("metrics output names a source after another namespace or subsystem: %s", body)
	}
}
