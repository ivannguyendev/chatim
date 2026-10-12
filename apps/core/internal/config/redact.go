package config

import (
	"log/slog"
	"reflect"
	"strings"
)

const redacted = "xxxxx"

var secretQueryKeys = []string{"pass", "secret", "token", "credential", "authmechanismproperties"}

var secretFields = map[string]func(Config) slog.Attr{
	"MongoURI":            func(c Config) slog.Attr { return slog.String("mongo_uri", RedactURL(c.MongoURI)) },
	"MongoPassword":       func(c Config) slog.Attr { return slog.Bool("mongo_auth", c.mongoAuth()) },
	"RedisPassword":       func(c Config) slog.Attr { return slog.Bool("redis_auth", c.RedisPassword != "") },
	"RedisDedupePassword": func(c Config) slog.Attr { return slog.Bool("redis_dedupe_auth", c.RedisDedupePassword != "") },
	"NATSURL":             func(c Config) slog.Attr { return slog.String("nats_url", RedactURL(c.NATSURL)) },
}

func (c Config) mongoAuth() bool { return c.MongoUser != "" || uriHasCredentials(c.MongoURI) }

func (c Config) LogValue() slog.Value {
	return slog.GroupValue(c.attrs(reflect.ValueOf(c), "")...)
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
