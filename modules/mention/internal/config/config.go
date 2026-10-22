package config

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

const (
	DefaultFetchBatch        = 512
	DefaultFetchWait         = 250 * time.Millisecond
	DefaultWorkers           = 8
	DefaultMaxAckPending     = 16384
	DefaultNotifyMaxAge      = 5 * time.Minute
	DefaultTargetsPage       = 1000
	MaxTargetsPage           = 5000
	DefaultRoomGroupsSyncMax = 5000
	DefaultRoomGroupsTTL     = time.Minute
	DefaultRoomGroupsRebuild = 10 * time.Minute
)

type Config struct {
	Enabled           bool
	EventStream       string
	EventSubjectRoot  string
	LiveRoot          string
	Replicas          int
	FetchBatch        int
	FetchWait         time.Duration
	Workers           int
	MaxAckPending     int
	NotifyMaxAge      time.Duration
	WsPublishStream   string
	WsPublishRoot     string
	TargetsPage       int
	HideReadTenants   []string
	RoomGroupsSyncMax int
	RoomGroupsTTL     time.Duration
	RoomGroupsRebuild time.Duration
}

func Defaults() Config { return Config{Enabled: true}.WithDefaults() }

func (c Config) WithDefaults() Config {
	c.FetchBatch = cmp.Or(c.FetchBatch, DefaultFetchBatch)
	c.FetchWait = cmp.Or(c.FetchWait, DefaultFetchWait)
	c.Workers = cmp.Or(c.Workers, DefaultWorkers)
	c.MaxAckPending = cmp.Or(c.MaxAckPending, DefaultMaxAckPending)
	c.NotifyMaxAge = cmp.Or(c.NotifyMaxAge, DefaultNotifyMaxAge)
	c.WsPublishStream = cmp.Or(c.WsPublishStream, wspublish.DefaultStream)
	c.WsPublishRoot = cmp.Or(c.WsPublishRoot, wspublish.DefaultRoot)
	c.TargetsPage = cmp.Or(c.TargetsPage, DefaultTargetsPage)
	c.RoomGroupsSyncMax = cmp.Or(c.RoomGroupsSyncMax, DefaultRoomGroupsSyncMax)
	c.RoomGroupsTTL = cmp.Or(c.RoomGroupsTTL, DefaultRoomGroupsTTL)
	c.RoomGroupsRebuild = cmp.Or(c.RoomGroupsRebuild, DefaultRoomGroupsRebuild)
	return c
}

func (c Config) StopTimeout() time.Duration {
	return c.WithDefaults().FetchWait + jsconsume.FetchExpiryGrace
}

func (c Config) Validate() error {
	c = c.WithDefaults()
	for _, check := range []func(Config) error{validateEvents, validateConsumer, validateNotify, validateRoomGroups} {
		if err := check(c); err != nil {
			return err
		}
	}
	return nil
}

func validateEvents(c Config) error {
	switch {
	case !subjectToken(c.EventStream) || !subjectToken(c.EventSubjectRoot) || !subjectToken(c.LiveRoot):
		return invalid("event stream %q, event subject root %q and live root %q must be single subject tokens", c.EventStream, c.EventSubjectRoot, c.LiveRoot)
	case c.Replicas <= 0:
		return invalid("ws publish stream replicas %d must be positive", c.Replicas)
	default:
		return nil
	}
}

func validateConsumer(c Config) error {
	switch {
	case c.FetchBatch <= 0 || c.FetchWait <= 0:
		return invalid("fetch batch %d and fetch wait %v must be positive", c.FetchBatch, c.FetchWait)
	case c.Workers < 1:
		return invalid("workers %d must be at least 1", c.Workers)
	case c.MaxAckPending < c.FetchBatch:
		return invalid("max ack pending %d must be at least the fetch batch %d", c.MaxAckPending, c.FetchBatch)
	default:
		return nil
	}
}

func validateNotify(c Config) error {
	if err := wspublish.ValidStream(c.WsPublishStream); err != nil {
		return err
	}
	if err := wspublish.ValidRoot(c.WsPublishRoot); err != nil {
		return err
	}
	switch {
	case c.NotifyMaxAge <= 0 || c.NotifyMaxAge > wspublish.DefaultDuplicates:
		return invalid("notify max age %v must be positive and within the ws publish duplicate window %v", c.NotifyMaxAge, wspublish.DefaultDuplicates)
	case c.TargetsPage < 1 || c.TargetsPage > MaxTargetsPage:
		return invalid("notify targets page %d must be 1..%d", c.TargetsPage, MaxTargetsPage)
	}
	for _, tenant := range c.HideReadTenants {
		if err := ids.ValidTenant(tenant); err != nil {
			return fmt.Errorf("hide read tenant %q: %w", tenant, err)
		}
	}
	return nil
}

func validateRoomGroups(c Config) error {
	if c.RoomGroupsSyncMax < 1 || c.RoomGroupsTTL <= 0 || c.RoomGroupsRebuild <= 0 {
		return invalid("room groups sync max %d, ttl %v and rebuild %v must be positive", c.RoomGroupsSyncMax, c.RoomGroupsTTL, c.RoomGroupsRebuild)
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: mention "+format, append([]any{apperr.ErrInvalidArgument}, args...)...)
}

func subjectToken(s string) bool { return s != "" && !strings.ContainsAny(s, ".*> \t\r\n") }
