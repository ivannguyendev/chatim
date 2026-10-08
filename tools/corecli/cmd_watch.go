package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const flushTimeout = 5 * time.Second

func watchCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	live := addLiveFlags(fs)
	tenant := fs.String("tenant", "e2e", "tenant of the room")
	room := fs.String("room", "", "room id")
	out := fs.String("out", "", "append events as JSON lines to this file instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := ids.ParseRoomID(*room); err != nil {
		return fmt.Errorf("-room: %w", err)
	}
	if err := singleTokens(*tenant, *live.root); err != nil {
		return err
	}
	w, err := openOutput(*out)
	if err != nil {
		return err
	}
	subject := *live.root + "." + *tenant + ".*." + *room + ".>"
	return errors.Join(watch(ctx, *live.url, subject, w), w.Close())
}

func watch(ctx context.Context, url, subject string, w io.Writer) error {
	nc, err := nats.Connect(url, nats.Name("chatim-corecli-watch"), nats.MaxReconnects(-1))
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer nc.Close()
	sub, err := nc.SubscribeSync(subject)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	if err := nc.FlushTimeout(flushTimeout); err != nil {
		return fmt.Errorf("confirm subscription %s: %w", subject, err)
	}
	fmt.Fprintln(os.Stderr, "watching", subject)
	enc := json.NewEncoder(w)
	for {
		msg, err := sub.NextMsgWithContext(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("next live event: %w", err)
		}
		var ev chatimv1.Event
		if err := proto.Unmarshal(msg.Data, &ev); err != nil {
			return fmt.Errorf("decode event on %s: %w", msg.Subject, err)
		}
		got, ok := e2e.EventOf(msg.Subject, &ev)
		if !ok {
			continue
		}
		if err := enc.Encode(got); err != nil {
			return fmt.Errorf("write event: %w", err)
		}
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func openOutput(path string) (io.WriteCloser, error) {
	if path == "" {
		return nopCloser{os.Stdout}, nil
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open events file: %w", err)
	}
	return f, nil
}
