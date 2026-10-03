package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxzan/gws"
)

type hub struct {
	gws.BuiltinEventHandler
	mu    sync.RWMutex
	conns map[*gws.Conn]struct{}
}

func (h *hub) OnOpen(c *gws.Conn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) OnClose(c *gws.Conn, _ error) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

func (h *hub) OnMessage(_ *gws.Conn, m *gws.Message) { _ = m.Close() }

func (h *hub) snapshot() []*gws.Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*gws.Conn, 0, len(h.conns))
	for c := range h.conns {
		out = append(out, c)
	}
	return out
}

func validateServerFlags(ports []string, every time.Duration, size int) error {
	for _, p := range ports {
		if n, err := strconv.ParseUint(p, 10, 16); err != nil || n == 0 {
			return fmt.Errorf("invalid -ports: %q", p)
		}
	}
	return errors.Join(requirePositive("every", every), requirePositive("size", size))
}

func runServer(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	portList := fs.String("ports", "9001,9002,9003,9004", "comma-separated listen ports")
	every := fs.Duration("every", 5*time.Second, "broadcast interval")
	size := fs.Int("size", 256, "broadcast payload bytes")
	duration := fs.Duration("duration", 0, "exit after this long (0 = until interrupted)")
	_ = fs.Parse(args)
	ports := splitList(*portList)
	if err := validateServerFlags(ports, *every, *size); err != nil {
		return err
	}
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	h := &hub{conns: map[*gws.Conn]struct{}{}}
	up := gws.NewUpgrader(h, &gws.ServerOption{
		ParallelEnabled:   false,
		PermessageDeflate: gws.PermessageDeflate{Enabled: false},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r)
		if err != nil {
			return
		}
		go c.ReadLoop()
	})
	servers := make([]*http.Server, 0, len(ports))
	for _, p := range ports {
		srv := &http.Server{Addr: ":" + p, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		servers = append(servers, srv)
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Println("listen:", err)
			}
		}()
	}
	fmt.Printf("listening on %s, broadcasting every %v\n", strings.Join(ports, ","), *every)

	tick := time.NewTicker(*every)
	defer tick.Stop()
	pending := new(atomic.Int64)
	for {
		select {
		case <-ctx.Done():
			for _, srv := range servers {
				_ = srv.Close()
			}
			return nil
		case <-tick.C:
			if n := pending.Load(); n > 0 {
				fmt.Printf("overlap: previous broadcast still has %d writes pending\n", n)
			}
			pending = broadcast(h.snapshot(), *size)
		}
	}
}

func broadcast(conns []*gws.Conn, size int) *atomic.Int64 {
	printIdleMemory(len(conns))
	payload := make([]byte, max(size, 8))
	start := time.Now()
	binary.BigEndian.PutUint64(payload, uint64(start.UnixNano()))
	pending := new(atomic.Int64)
	pending.Store(int64(len(conns)))
	var failed atomic.Int64
	written := func(err error) {
		if err != nil {
			failed.Add(1)
		}
		if pending.Add(-1) == 0 {
			fmt.Printf("  last write done %v after broadcast start (write errors=%d)\n", time.Since(start), failed.Load())
		}
	}
	b := gws.NewBroadcaster(gws.OpcodeBinary, payload)
	enqueued := 0
	for _, c := range conns {
		if err := b.Broadcast(c, written); err != nil {
			written(err)
			continue
		}
		enqueued++
	}
	_ = b.Close()
	fmt.Printf("  enqueued %d frames in %v\n", enqueued, time.Since(start))
	return pending
}

func printIdleMemory(conns int) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	perConn := 0.0
	if conns > 0 {
		perConn = float64(ms.HeapInuse+ms.StackInuse) / float64(conns) / 1024
	}
	fmt.Printf("conns=%d goroutines=%d heap=%.0fMB stack=%.0fMB sys=%.0fMB per-conn=%.1fKB\n",
		conns, runtime.NumGoroutine(), mbytes(ms.HeapInuse), mbytes(ms.StackInuse), mbytes(ms.Sys), perConn)
}

func mbytes(b uint64) float64 { return float64(b) / (1 << 20) }
