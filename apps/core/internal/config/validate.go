package config

import (
	"errors"
	"regexp"
	"time"
)

var (
	streamNamePattern  = regexp.MustCompile(`^[A-Z0-9_]+$`)
	subjectRootPattern = regexp.MustCompile(`^[a-z0-9_]+$`)
)

func (c Config) validate() error {
	rules := []struct {
		ok  bool
		msg string
	}{
		{c.MongoURI != "", "MONGO_URI is required"},
		{c.RedisDB >= 0, "REDIS_DB must not be negative"},
		{c.StreamReplicas > 0, "EVT_STREAM_REPLICAS must be positive"},
		{c.FlushMaxBatch > 0, "FLUSH_MAX_BATCH must be positive"},
		{c.FlushShards > 0, "FLUSH_SHARDS must be positive"},
		{c.Mailbox > 0, "ACTOR_MAILBOX must be positive"},
		{c.MaxInflight > 0, "CORE_MAX_INFLIGHT must be positive"},
		{c.FlushWindow > 0, "FLUSH_WINDOW must be positive"},
		{c.ActorIdle > 0, "ACTOR_IDLE must be positive"},
		{c.RequestDeadline > 0, "CORE_REQUEST_DEADLINE must be positive"},
		{c.DrainDelay > 0, "CORE_DRAIN_DELAY must be positive"},
		{c.GRPCShutdown > 0, "CORE_GRPC_SHUTDOWN must be positive"},
		{c.PublisherDrain > 0, "CORE_PUBLISHER_DRAIN must be positive"},
		{c.ShutdownBudget > 0, "CORE_SHUTDOWN_BUDGET must be positive"},
		{streamNamePattern.MatchString(c.StreamName), "EVT_STREAM must match " + streamNamePattern.String()},
		{subjectRootPattern.MatchString(c.SubjectRoot), "EVT_SUBJECT_ROOT must match " + subjectRootPattern.String()},
		{subjectRootPattern.MatchString(c.LiveRoot), "EVT_LIVE_ROOT must match " + subjectRootPattern.String()},
		{c.SubjectRoot != c.LiveRoot, "EVT_SUBJECT_ROOT and EVT_LIVE_ROOT must differ"},
		{c.RequestDeadline < c.GRPCShutdown, "CORE_REQUEST_DEADLINE must be shorter than CORE_GRPC_SHUTDOWN"},
		{
			fitsWithin(c.ShutdownBudget, c.DrainDelay, c.GRPCShutdown, c.PublisherDrain),
			"CORE_DRAIN_DELAY + CORE_GRPC_SHUTDOWN + CORE_PUBLISHER_DRAIN must be shorter than CORE_SHUTDOWN_BUDGET",
		},
	}
	var errs []error
	for _, r := range rules {
		if !r.ok {
			errs = append(errs, errors.New(r.msg))
		}
	}
	return errors.Join(errs...)
}

func fitsWithin(budget time.Duration, phases ...time.Duration) bool {
	for _, d := range phases {
		budget -= d
		if budget <= 0 {
			return false
		}
	}
	return true
}
