package api_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"testing"
	"time"
)

func TestIdlePollsReuseOneHTTP1Connection(t *testing.T) {
	f := newFeedHarness(t, time.Second)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: f.handler, ReadHeaderTimeout: 200 * time.Millisecond, ReadTimeout: 200 * time.Millisecond, WriteTimeout: 300 * time.Millisecond, IdleTimeout: 5 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	transport := &http.Transport{ForceAttemptHTTP2: false, MaxIdleConnsPerHost: 1}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		_ = srv.Close()
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Serve: %v", err)
		}
	})
	for i, wantReused := range []bool{false, true} {
		var reused bool
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
		req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), http.MethodGet, "http://"+ln.Addr().String()+feedPath+"?wait=1", nil)
		req.Header.Set("Authorization", "Bearer "+nodeSecret)
		began := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 1 || reused != wantReused || time.Since(began) < time.Second {
			t.Fatalf("poll %d = %d %s proto %d reused %v after %v; want 200 over HTTP/1.1 held 1s, reused %v", i, resp.StatusCode, body, resp.ProtoMajor, reused, time.Since(began), wantReused)
		}
	}
}
