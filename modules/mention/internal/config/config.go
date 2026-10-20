package config

import (
	"cmp"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

const (
	DefaultFetchBatch         = 512
	DefaultFetchWait          = 250 * time.Millisecond
	DefaultWorkers            = 8
	DefaultMaxAckPending      = 16384
	DefaultNotifyMaxAge       = 5 * time.Minute
	DefaultNotifyStream       = "CHATIM_NOTIFY"
	DefaultNotifySubjectRoot  = "notify"
	DefaultNotifyStreamMaxAge = 24 * time.Hour
	DefaultNotifyDuplicates   = 6 * time.Minute
	DefaultRoomGroupsSyncMax  = 5000
	DefaultRoomGroupsTTL      = time.Minute
	DefaultRoomGroupsRebuild  = 10 * time.Minute
)

var (
	notifyStreamName = regexp.MustCompile(`^[A-Z0-9_]+$`)
	notifyRootName   = regexp.MustCompile(`^[a-z0-9_]+$`)
)

type Config struct {
	Enabled            bool
	EventStream        string
	EventSubjectRoot   string
	LiveRoot           string
	Replicas           int
	FetchBatch         int
	FetchWait          time.Duration
	Workers            int
	MaxAckPending      int
	NotifyMaxAge       time.Duration
	NotifyStream       string
	NotifySubjectRoot  string
	NotifyMaxStreamAge time.Duration
	NotifyDuplicates   time.Duration
	HideReadTenants    []string
	RoomGroupsSyncMax  int
	RoomGroupsTTL      time.Duration
	RoomGroupsRebuild  time.Duration
}

func Defaults() Config { return Config{Enabled: true}.WithDefaults() }

func (c Config) WithDefaults() Config {
	c.FetchBatch = cmp.Or(c.FetchBatch, DefaultFetchBatch)
	c.FetchWait = cmp.Or(c.FetchWait, DefaultFetchWait)
	c.Workers = cmp.Or(c.Workers, DefaultWorkers)
	c.MaxAckPending = cmp.Or(c.MaxAckPending, DefaultMaxAckPending)
	c.NotifyMaxAge = cmp.Or(c.NotifyMaxAge, DefaultNotifyMaxAge)
	c.NotifyStream = cmp.Or(c.NotifyStream, DefaultNotifyStream)
	c.NotifySubjectRoot = cmp.Or(c.NotifySubjectRoot, DefaultNotifySubjectRoot)
	c.NotifyMaxStreamAge = cmp.Or(c.NotifyMaxStreamAge, DefaultNotifyStreamMaxAge)
	c.NotifyDuplicates = cmp.Or(c.NotifyDuplicates, DefaultNotifyDuplicates)
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
		return invalid("notify stream replicas %d must be positive", c.Replicas)
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
	switch {
	case !notifyStreamName.MatchString(c.NotifyStream) || !notifyRootName.MatchString(c.NotifySubjectRoot):
		return invalid("notify stream %q must match %s and subject root %q must match %s", c.NotifyStream, notifyStreamName, c.NotifySubjectRoot, notifyRootName)
	case c.NotifyMaxAge <= 0 || c.NotifyMaxStreamAge <= 0 || c.NotifyDuplicates <= 0:
		return invalid("notify max age %v, stream max age %v and duplicate window %v must be positive", c.NotifyMaxAge, c.NotifyMaxStreamAge, c.NotifyDuplicates)
	case c.NotifyMaxAge > c.NotifyDuplicates:
		return invalid("notify max age %v exceeds the duplicate window %v", c.NotifyMaxAge, c.NotifyDuplicates)
	case c.NotifyDuplicates > c.NotifyMaxStreamAge:
		return invalid("notify duplicate window %v exceeds the stream max age %v", c.NotifyDuplicates, c.NotifyMaxStreamAge)
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
