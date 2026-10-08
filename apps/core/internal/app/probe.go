package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	probeTimeout   = 2 * time.Second
	probeBodyLimit = 4 << 10
)

func Probe(ctx context.Context, adminAddr string) error {
	target := "http://" + loopbackAddr(adminAddr) + "/readyz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return fmt.Errorf("probe %s: %w", target, err)
	}
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe %s: %w", target, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, probeBodyLimit))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe %s: status %d", target, resp.StatusCode)
	}
	return nil
}

func loopbackAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
