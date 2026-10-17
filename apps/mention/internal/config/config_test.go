package config_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/config"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

func linked() config.Config {
	return config.Config{EventStream: "CHATIM_EVT", EventSubjectRoot: "evt", LiveRoot: "live", Replicas: 1}
}

func TestDefaults(t *testing.T) {
	c := config.Defaults()
	want := config.Config{
		Enabled:            true,
		FetchBatch:         512,
		FetchWait:          250 * time.Millisecond,
		Workers:            8,
		MaxAckPending:      16384,
		NotifyMaxAge:       5 * time.Minute,
		NotifyStream:       "CHATIM_NOTIFY",
		NotifySubjectRoot:  "notify",
		NotifyMaxStreamAge: 24 * time.Hour,
		NotifyDuplicates:   6 * time.Minute,
		RoomGroupsSyncMax:  5000,
		RoomGroupsTTL:      time.Minute,
		RoomGroupsRebuild:  10 * time.Minute,
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("defaults = %+v, want %+v", c, want)
	}
}

func TestZeroFieldsTakeDefaults(t *testing.T) {
	c := linked().WithDefaults()
	d := config.Defaults()
	if c.FetchBatch != d.FetchBatch || c.NotifyStream != d.NotifyStream || c.RoomGroupsRebuild != d.RoomGroupsRebuild {
		t.Fatalf("with defaults = %+v", c)
	}
	if err := linked().Validate(); err != nil {
		t.Fatalf("validate linked config: %v", err)
	}
}

func TestStopTimeout(t *testing.T) {
	c := linked()
	if got, want := c.StopTimeout(), 250*time.Millisecond+jsconsume.FetchExpiryGrace; got != want {
		t.Fatalf("stop timeout = %v, want %v", got, want)
	}
	c.FetchWait = time.Second
	if got, want := c.StopTimeout(), time.Second+jsconsume.FetchExpiryGrace; got != want {
		t.Fatalf("stop timeout = %v, want %v", got, want)
	}
}

func TestValidateRules(t *testing.T) {
	cases := map[string]func(*config.Config){
		"event stream empty":            func(c *config.Config) { c.EventStream = "" },
		"event stream with dot":         func(c *config.Config) { c.EventStream = "CHATIM.EVT" },
		"event subject root wildcard":   func(c *config.Config) { c.EventSubjectRoot = "evt.*" },
		"live root empty":               func(c *config.Config) { c.LiveRoot = "" },
		"replicas zero":                 func(c *config.Config) { c.Replicas = 0 },
		"fetch batch negative":          func(c *config.Config) { c.FetchBatch = -1 },
		"fetch wait negative":           func(c *config.Config) { c.FetchWait = -time.Millisecond },
		"workers negative":              func(c *config.Config) { c.Workers = -1 },
		"max ack pending below batch":   func(c *config.Config) { c.FetchBatch = 1024; c.MaxAckPending = 512 },
		"notify max age negative":       func(c *config.Config) { c.NotifyMaxAge = -time.Second },
		"notify max age over dupes":     func(c *config.Config) { c.NotifyMaxAge = 7 * time.Minute },
		"notify stream lowercase":       func(c *config.Config) { c.NotifyStream = "chatim_notify" },
		"notify stream with dot":        func(c *config.Config) { c.NotifyStream = "CHATIM.NOTIFY" },
		"notify root uppercase":         func(c *config.Config) { c.NotifySubjectRoot = "Notify" },
		"notify root with dot":          func(c *config.Config) { c.NotifySubjectRoot = "notify.x" },
		"notify stream age negative":    func(c *config.Config) { c.NotifyMaxStreamAge = -time.Hour },
		"notify duplicates negative":    func(c *config.Config) { c.NotifyDuplicates = -time.Minute },
		"notify duplicates over age":    func(c *config.Config) { c.NotifyMaxStreamAge = 5 * time.Minute; c.NotifyMaxAge = time.Minute },
		"hide read tenant invalid":      func(c *config.Config) { c.HideReadTenants = []string{"t1", "Bad Tenant"} },
		"hide read tenant empty":        func(c *config.Config) { c.HideReadTenants = []string{""} },
		"room groups sync max negative": func(c *config.Config) { c.RoomGroupsSyncMax = -1 },
		"room groups ttl negative":      func(c *config.Config) { c.RoomGroupsTTL = -time.Second },
		"room groups rebuild negative":  func(c *config.Config) { c.RoomGroupsRebuild = -time.Second },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := linked()
			mutate(&c)
			if err := c.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("validate = %v, want invalid argument", err)
			}
		})
	}
}

func TestValidateAcceptsEdges(t *testing.T) {
	c := linked()
	c.FetchBatch = 100
	c.MaxAckPending = 100
	c.Workers = 1
	c.NotifyMaxAge = 6 * time.Minute
	c.NotifyMaxStreamAge = 6 * time.Minute
	c.RoomGroupsSyncMax = 1
	c.HideReadTenants = []string{"t1", "tenant_2"}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate edges: %v", err)
	}
}
