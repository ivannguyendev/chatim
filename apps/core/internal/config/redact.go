package config

import (
	"log/slog"
	"strings"
)

const redacted = "xxxxx"

var secretQueryKeys = []string{"pass", "secret", "token", "credential", "authmechanismproperties"}

func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("core_id", c.CoreID),
		slog.String("grpc_addr", c.GRPCAddr),
		slog.String("advertise_addr", c.AdvertiseAddr),
		slog.String("admin_addr", c.AdminAddr),
		slog.String("mongo_uri", RedactURL(c.MongoURI)),
		slog.String("mongo_db", c.MongoDB),
		slog.Bool("mongo_auth", c.MongoUser != "" || uriHasCredentials(c.MongoURI)),
		slog.String("redis_addr", c.RedisAddr),
		slog.Int("redis_db", c.RedisDB),
		slog.Bool("redis_auth", c.RedisPassword != ""),
		slog.String("redis_dedupe_addr", c.RedisDedupeAddr),
		slog.Int("redis_dedupe_db", c.RedisDedupeDB),
		slog.Bool("redis_dedupe_auth", c.RedisDedupePassword != ""),
		slog.String("nats_url", RedactURL(c.NATSURL)),
		slog.String("stream", c.Stream.Name),
		slog.String("subject_root", c.Stream.SubjectRoot),
		slog.String("live_root", c.Stream.LiveRoot),
		slog.Duration("request_deadline", c.RequestDeadline),
		slog.Int("max_inflight", c.MaxInflight),
		slog.Int("flush_shards", c.Flush.Shards),
		slog.Int("publish_shards", c.Publish.Shards),
		slog.Int("actor_max", c.Actor.MaxActors),
		slog.Duration("shutdown_budget", c.ShutdownBudget),
	)
}

func (c Config) String() string { return c.LogValue().String() }

func RedactURL(raw string) string {
	if raw == "" {
		return ""
	}
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return redacted
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = redacted + "@" + rest[at+1:]
	}
	if base, query, ok := strings.Cut(rest, "?"); ok {
		rest = base + "?" + redactQuery(query)
	}
	return scheme + "://" + rest
}

func uriHasCredentials(raw string) bool {
	_, rest, ok := strings.Cut(raw, "://")
	base, _, _ := strings.Cut(rest, "?")
	return ok && strings.Contains(base, "@")
}

func redactQuery(query string) string {
	pairs := strings.Split(query, "&")
	for i, pair := range pairs {
		key, _, _ := strings.Cut(pair, "=")
		if secretKey(key) {
			pairs[i] = key + "=" + redacted
		}
	}
	return strings.Join(pairs, "&")
}

func secretKey(key string) bool {
	key = strings.ToLower(key)
	for _, s := range secretQueryKeys {
		if strings.Contains(key, s) {
			return true
		}
	}
	return false
}
