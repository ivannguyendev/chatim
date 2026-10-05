package config

import (
	"errors"
	"fmt"
	"net"
	"regexp"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
)

var (
	streamNamePattern  = regexp.MustCompile(`^[A-Z0-9_]+$`)
	subjectRootPattern = regexp.MustCompile(`^[a-z0-9_]+$`)
)

const stopPhases = "CORE_DRAIN_DELAY + CORE_GRPC_SHUTDOWN + RECONCILE_DRAIN + 1s + CORE_REQUEST_DEADLINE (router drain) + " +
	"2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT (flusher drain) + CORE_PUBLISHER_DRAIN + slot release + client close"

type rule struct {
	ok  bool
	msg string
}

func (c Config) validate() error {
	plan := c.StopPlan()
	rules := []rule{
		{c.MongoURI != "", "MONGO_URI is required"},
		{c.MongoUser == "" || !uriHasCredentials(c.MongoURI), "MONGO_USER must not be set when MONGO_URI carries credentials"},
		{c.MongoPassword == "" || c.MongoUser != "", "MONGO_PASSWORD needs MONGO_USER"},
		{validAdvertiseAddr(c.AdvertiseAddr), "CORE_ADVERTISE_ADDR must be host:port with a host"},
		{streamNamePattern.MatchString(c.Stream.Name), "EVT_STREAM must match " + streamNamePattern.String()},
		{subjectRootPattern.MatchString(c.Stream.SubjectRoot), "EVT_SUBJECT_ROOT must match " + subjectRootPattern.String()},
		{subjectRootPattern.MatchString(c.Stream.LiveRoot), "EVT_LIVE_ROOT must match " + subjectRootPattern.String()},
		{c.RequestDeadline < c.GRPCShutdown, "CORE_REQUEST_DEADLINE must be shorter than CORE_GRPC_SHUTDOWN"},
		{c.QueueWait <= c.RequestDeadline/10, "CORE_QUEUE_WAIT must be at most a tenth of CORE_REQUEST_DEADLINE"},
		{c.Flush.InsertTimeout < c.RequestDeadline, "FLUSH_INSERT_TIMEOUT must be shorter than CORE_REQUEST_DEADLINE"},
		{c.Dedupe.Timeout <= c.RequestDeadline/10, "REDIS_OP_TIMEOUT must be at most a tenth of CORE_REQUEST_DEADLINE"},
		{c.Actor.MaxGroup <= c.Flush.MaxBatch, "ACTOR_MAX_GROUP must not exceed FLUSH_MAX_BATCH"},
		{c.Publish.AckTimeout < c.PublisherDrain, "PUB_ACK_TIMEOUT must be shorter than CORE_PUBLISHER_DRAIN"},
		{!c.ReconcileEnabled || c.Reconcile.Delay < c.Stream.Duplicates, "RECONCILE_DELAY must be shorter than EVT_STREAM_DUPLICATES"},
		{!c.ReconcileEnabled || c.Reconcile.Delay > publish.MarkDeadline(c.Publish.AckTimeout), "RECONCILE_DELAY must be longer than PUB_ACK_TIMEOUT plus the ack mark window and timeout"},
		{plan.fitsWithin(c.ShutdownBudget), fmt.Sprintf("%s = %v must be shorter than CORE_SHUTDOWN_BUDGET %v", stopPhases, plan.total(), c.ShutdownBudget)},
	}
	var errs []error
	for _, r := range rules {
		if !r.ok {
			errs = append(errs, errors.New(r.msg))
		}
	}
	return errors.Join(append(errs, c.componentErrors()...)...)
}

func (c Config) componentErrors() []error {
	parts := []struct {
		keys string
		err  error
	}{
		{"FLUSH_*", c.Flush.Validate()},
		{"ACTOR_*, CID_PENDING_TTL, CORE_REQUEST_DEADLINE", c.Actor.Validate()},
		{"CORE_ID, CID_*, REDIS_OP_TIMEOUT, REDIS_COOLDOWN", c.Dedupe.Validate()},
		{"CID_BATCH_*", c.CIDBatch.Validate()},
		{"PUB_*, EVT_SUBJECT_ROOT", c.Publish.Validate()},
		{"EVT_*", c.Stream.Validate()},
		{"SLOT_*, CORE_ID", c.Slot.Validate()},
		{"EVT_ACK_MARK_TTL, REDIS_OP_TIMEOUT, REDIS_COOLDOWN", c.AckMarks.Validate()},
	}
	if c.ReconcileEnabled {
		parts = append(parts, struct {
			keys string
			err  error
		}{"RECONCILE_*", c.Reconcile.Validate()})
	}
	var errs []error
	for _, part := range parts {
		if part.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", part.keys, part.err))
		}
	}
	return errs
}

func validAdvertiseAddr(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	return err == nil && host != "" && port != ""
}
