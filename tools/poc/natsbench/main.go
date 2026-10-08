package main

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const stream = "POC_EVT"

type config struct {
	url, monitor     string
	subs, conns      int
	rate, publishers int
	size             int
	duration         time.Duration
}

func main() { os.Exit(realMain()) }

func realMain() int {
	var c config
	flag.StringVar(&c.url, "url", cmp.Or(os.Getenv("NATS_URL"), "nats://chatim-nats:4222"), "NATS URL (env NATS_URL)")
	flag.StringVar(&c.monitor, "monitor", cmp.Or(os.Getenv("NATS_MONITOR_URL"), "http://chatim-nats:8222"), "NATS monitoring URL (env NATS_MONITOR_URL)")
	flag.IntVar(&c.subs, "subs", 100_000, "room subscriptions across all gateway connections")
	flag.IntVar(&c.conns, "conns", 4, "gateway connections")
	flag.IntVar(&c.rate, "rate", 5000, "events per second")
	flag.IntVar(&c.publishers, "publishers", 8, "parallel publishers (≈ core flushers)")
	flag.IntVar(&c.size, "size", 256, "event payload bytes")
	flag.DurationVar(&c.duration, "duration", 30*time.Second, "publish duration")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, c); err != nil {
		fmt.Fprintln(os.Stderr, "natsbench:", err)
		return 1
	}
	return 0
}

func (c config) interval() time.Duration {
	return time.Duration(float64(time.Second) * float64(c.publishers) / float64(c.rate))
}

func (c config) validate() error {
	if c.subs <= 0 || c.conns <= 0 || c.rate <= 0 || c.publishers <= 0 || c.duration <= 0 {
		return errors.New("invalid flags: -subs, -conns, -rate, -publishers and -duration must be positive")
	}
	if c.interval() <= 0 {
		return errors.New("rate too high for publisher count")
	}
	return nil
}

func run(ctx context.Context, c config) error {
	if err := c.validate(); err != nil {
		return err
	}
	nc, err := nats.Connect(c.url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	if err := js.DeleteStream(ctx, stream); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		return fmt.Errorf("delete old stream: %w", err)
	}
	_, err = js.CreateStream(ctx, jetstream.StreamConfig{
		Name:       stream,
		Subjects:   []string{"evt.>"},
		Storage:    jetstream.FileStorage,
		MaxAge:     time.Hour,
		Duplicates: 2 * time.Minute,
		RePublish: &jetstream.RePublish{
			Source:      "evt.*.*.*.*",
			Destination: "live.{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}",
		},
	})
	if err != nil {
		return fmt.Errorf("create stream: %w", err)
	}
	if err := checkRepublish(ctx, nc, js); err != nil {
		return err
	}
	fmt.Println("republish transform: PASS")
	return load(ctx, c, js)
}

func checkRepublish(ctx context.Context, nc *nats.Conn, js jetstream.JetStream) error {
	sub, err := nc.SubscribeSync("live.t1.message.42.evt.msg_created")
	if err != nil {
		return err
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := nc.Flush(); err != nil {
		return err
	}
	if _, err := js.Publish(ctx, "evt.t1.message.42.msg_created", stamp(16)); err != nil {
		return fmt.Errorf("publish probe: %w", err)
	}
	if _, err := sub.NextMsg(2 * time.Second); err != nil {
		return fmt.Errorf("republish transform: FAIL (%w)", err)
	}
	return nil
}

func stamp(size int) []byte {
	b := make([]byte, max(size, 8))
	binary.BigEndian.PutUint64(b, uint64(time.Now().UnixNano()))
	return b
}

func serverMem(ctx context.Context, monitor string) string {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, monitor+"/varz", nil)
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	defer resp.Body.Close()
	var v struct {
		Mem  int64  `json:"mem"`
		Subs uint32 `json:"subscriptions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "unknown (" + err.Error() + ")"
	}
	return fmt.Sprintf("server mem=%.0fMB subscriptions=%d", float64(v.Mem)/(1<<20), v.Subs)
}
