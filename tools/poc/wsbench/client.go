package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxzan/gws"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

type receiver struct {
	gws.BuiltinEventHandler
	lat *latency.Recorder
}

func (r *receiver) OnMessage(_ *gws.Conn, m *gws.Message) {
	defer func() { _ = m.Close() }()
	if b := m.Bytes(); len(b) >= 8 {
		r.lat.Add(time.Since(time.Unix(0, int64(binary.BigEndian.Uint64(b)))))
	}
}

func runClient(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	addrs := fs.String("addrs", "ws://chatim-wsbench-server:9001/ws", "comma-separated server URLs")
	total := fs.Int("conns", 10_000, "connections to open")
	dialRate := fs.Int("dial-rate", 2000, "new connections per second")
	duration := fs.Duration("duration", 0, "exit after this long (0 = until interrupted)")
	_ = fs.Parse(args)
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	urls := strings.Split(*addrs, ",")
	rec := &receiver{lat: &latency.Recorder{}}
	var mu sync.Mutex
	var open []*gws.Conn
	var failed atomic.Int64
	go func() {
		pace := time.NewTicker(time.Second / time.Duration(max(*dialRate, 1)))
		defer pace.Stop()
		for i := 0; i < *total && ctx.Err() == nil; i++ {
			<-pace.C
			go func(url string) {
				c, _, err := gws.NewClient(rec, &gws.ClientOption{Addr: url})
				if err != nil {
					failed.Add(1)
					return
				}
				mu.Lock()
				open = append(open, c)
				mu.Unlock()
				c.ReadLoop()
			}(strings.TrimSpace(urls[i%len(urls)]))
		}
	}()

	report := time.NewTicker(5 * time.Second)
	defer report.Stop()
	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			for _, c := range open {
				_ = c.WriteClose(1000, nil)
			}
			mu.Unlock()
			return nil
		case <-report.C:
			mu.Lock()
			n := len(open)
			mu.Unlock()
			fmt.Printf("connected=%d failed=%d broadcast latency (last 5s): %v\n", n, failed.Load(), rec.lat.Summary())
			rec.lat.Reset()
		}
	}
}
