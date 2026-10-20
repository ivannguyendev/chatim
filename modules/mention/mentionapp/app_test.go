package mentionapp_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/modules/mention/mentionapp"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

type fakeJS struct{ jetstream.JetStream }

type fakeCore struct{ mentionapp.CoreReader }

func config() mentionapp.Config {
	c := mentionapp.DefaultConfig()
	c.EventStream, c.EventSubjectRoot, c.LiveRoot, c.Replicas = "CHATIM_EVT", "evt", "live", 1
	return c
}

func offlineDB(t *testing.T) *mongo.Database {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:1").SetServerSelectionTimeout(time.Millisecond))
	if err != nil {
		t.Fatalf("mongo client: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client.Database("mention_app_test")
}

func deps(t *testing.T) mentionapp.Deps {
	t.Helper()
	return mentionapp.Deps{
		Mongo:    offlineDB(t),
		JS:       fakeJS{},
		NotifyJS: fakeJS{},
		NATS:     &nats.Conn{},
		Core:     fakeCore{},
		Log:      slog.New(slog.DiscardHandler),
	}
}

func TestNewHoldsValidConfig(t *testing.T) {
	app, err := mentionapp.New(config(), deps(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if got, want := app.StopTimeout(), 250*time.Millisecond+jsconsume.FetchExpiryGrace; got != want {
		t.Fatalf("stop timeout = %v, want %v", got, want)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	c := mentionapp.Config{EventStream: "CHATIM_EVT", EventSubjectRoot: "evt", LiveRoot: "live", Replicas: 1, FetchWait: time.Second}
	app, err := mentionapp.New(c, deps(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if got, want := app.StopTimeout(), time.Second+jsconsume.FetchExpiryGrace; got != want {
		t.Fatalf("stop timeout = %v, want %v", got, want)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	c := config()
	c.Workers = -1
	if _, err := mentionapp.New(c, deps(t)); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("new = %v, want invalid argument", err)
	}
}

func TestNewRejectsMissingDeps(t *testing.T) {
	cases := map[string]func(*mentionapp.Deps){
		"mongo":     func(d *mentionapp.Deps) { d.Mongo = nil },
		"js":        func(d *mentionapp.Deps) { d.JS = nil },
		"notify js": func(d *mentionapp.Deps) { d.NotifyJS = nil },
		"nats":      func(d *mentionapp.Deps) { d.NATS = nil },
		"core":      func(d *mentionapp.Deps) { d.Core = nil },
		"log":       func(d *mentionapp.Deps) { d.Log = nil },
	}
	for name, drop := range cases {
		t.Run(name, func(t *testing.T) {
			d := deps(t)
			drop(&d)
			if _, err := mentionapp.New(config(), d); !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("new = %v, want invalid argument", err)
			}
		})
	}
}

func TestActions(t *testing.T) {
	if mentionapp.ReadMembers != "read_members" || mentionapp.ReadHistory != "read_history" {
		t.Fatalf("actions = %q, %q", mentionapp.ReadMembers, mentionapp.ReadHistory)
	}
}
