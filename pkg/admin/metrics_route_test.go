package admin_test

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/admin"
)

func TestMetricsRouteServesOnlyAConfiguredHandler(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("chatim_up 1\n")) })
	cases := map[string]struct {
		handler http.Handler
		code    int
	}{
		"configured": {h, http.StatusOK},
		"absent":     {nil, http.StatusNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			srv := admin.New(admin.Config{Metrics: tc.handler}, slog.New(slog.DiscardHandler))
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.ServeListener(ctx, lis) }()
			t.Cleanup(func() {
				cancel()
				<-done
			})
			client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
			resp, err := client.Get("http://" + lis.Addr().String() + "/metrics")
			if err != nil {
				t.Fatalf("GET /metrics: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tc.code {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.code)
			}
		})
	}
}
