package config

import (
	"errors"
	"fmt"
	"net"
	"regexp"
)

var (
	streamNamePattern  = regexp.MustCompile(`^[A-Z0-9_]+$`)
	subjectRootPattern = regexp.MustCompile(`^[a-z0-9_]+$`)
)

const stopPhases = "CORE_DRAIN_DELAY + CORE_GRPC_SHUTDOWN + 1s (sweeper) + max(PROJECTION_FETCH_WAIT, MENTION_FETCH_WAIT) + 1s (projections and mention) + 500ms + 1s (timer consumer) + CORE_REQUEST_DEADLINE (router drain) + " +
	"2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT (flusher drain) + CORE_PUBLISHER_DRAIN + 1s (state clear) + slot release + client close"

const minAgeRule = "SWEEP_MIN_AGE must be longer than PUB_ACK_TIMEOUT + STATE_CLEAR_FLUSH + STATE_CLEAR_TIMEOUT"

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
		{streamNamePattern.MatchString(c.Timer.Name), "TIMER_STREAM must match " + streamNamePattern.String()},
		{subjectRootPattern.MatchString(c.Timer.SubjectRoot), "TIMER_SUBJECT_ROOT must match " + subjectRootPattern.String()},
		{c.Timer.Name != c.Stream.Name, "TIMER_STREAM must differ from EVT_STREAM"},
		{c.Timer.SubjectRoot != c.Stream.SubjectRoot && c.Timer.SubjectRoot != c.Stream.LiveRoot, "TIMER_SUBJECT_ROOT must differ from EVT_SUBJECT_ROOT and EVT_LIVE_ROOT"},
		{c.RequestDeadline < c.GRPCShutdown, "CORE_REQUEST_DEADLINE must be shorter than CORE_GRPC_SHUTDOWN"},
		{c.QueueWait <= c.RequestDeadline/10, "CORE_QUEUE_WAIT must be at most a tenth of CORE_REQUEST_DEADLINE"},
		{c.Flush.InsertTimeout < c.RequestDeadline, "FLUSH_INSERT_TIMEOUT must be shorter than CORE_REQUEST_DEADLINE"},
		{c.Dedupe.Timeout <= c.RequestDeadline/10, "REDIS_OP_TIMEOUT must be at most a tenth of CORE_REQUEST_DEADLINE"},
		{c.Actor.MaxGroup <= c.Flush.MaxBatch, "ACTOR_MAX_GROUP must not exceed FLUSH_MAX_BATCH"},
		{c.Publish.AckTimeout < c.PublisherDrain, "PUB_ACK_TIMEOUT must be shorter than CORE_PUBLISHER_DRAIN"},
		{c.Sweep.MinAge > c.Publish.AckTimeout+c.StateClear.Flush+c.StateClear.Timeout, minAgeRule},
		{c.Sweep.MinAge+c.Sweep.Interval < c.Stream.Duplicates, "SWEEP_MIN_AGE + SWEEP_INTERVAL must be shorter than EVT_STREAM_DUPLICATES"},
		{c.Sweep.Lookback > c.RequestDeadline+sweepLookbackSlack, "SWEEP_LOOKBACK must be longer than CORE_REQUEST_DEADLINE + 10s"},
		{c.Sweep.Lookback > c.Sweep.MinAge+sweepLookbackSlack, "SWEEP_LOOKBACK must be longer than SWEEP_MIN_AGE + 10s"},
		{c.MemberCountCheckDelay > c.RequestDeadline, "MEMBER_COUNT_CHECK_DELAY must be longer than CORE_REQUEST_DEADLINE"},
		{c.MessageCountCheckDelay > c.RequestDeadline, "MESSAGE_COUNT_CHECK_DELAY must be longer than CORE_REQUEST_DEADLINE"},
		{c.RoomList.TailCacheBytes >= minRoomTailCacheBytes, fmt.Sprintf("ROOM_TAIL_CACHE_BYTES must be at least %d (1MB)", minRoomTailCacheBytes)},
		{plan.fitsWithin(c.ShutdownBudget), fmt.Sprintf("%s = %v must be shorter than CORE_SHUTDOWN_BUDGET %v", stopPhases, plan.Total(), c.ShutdownBudget)},
	}
	rules = append(rules, c.mentionRules()...)
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
		{"ACTOR_*, MENTION_TARGETS_MAX, CID_PENDING_TTL, CORE_REQUEST_DEADLINE", c.Actor.Validate()},
		{"CORE_ID, CID_*, REDIS_OP_TIMEOUT, REDIS_COOLDOWN", c.Dedupe.Validate()},
		{"CID_BATCH_*", c.CIDBatch.Validate()},
		{"PUB_*, EVT_SUBJECT_ROOT", c.Publish.Validate()},
		{"EVT_*", c.Stream.Validate()},
		{"TIMER_*, EVT_STREAM_REPLICAS", c.Timer.Validate()},
		{"SLOT_*, CORE_ID", c.Slot.Validate()},
		{"STATE_CLEAR_*", c.StateClear.Validate()},
		{"SWEEP_*, EVT_SUBJECT_ROOT, SLOT_TICK, PUB_ACK_TIMEOUT", c.Sweep.Validate()},
		{"PROJECTION_*, EVT_STREAM, EVT_SUBJECT_ROOT", c.Projection.Validate()},
		{"REACTION_EMOJIS, PIN_LIMIT", errors.Join(c.Interactions.Validate(), c.Limits.Validate())},
		{"MEMBER_BATCH_MAX", c.Members.Validate()},
		{"MENTION_*, NOTIFY_*", c.mentionErrors()},
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
