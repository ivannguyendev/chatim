package config

import (
	"cmp"
	"log/slog"
	"reflect"
	"slices"
	"strings"

	"github.com/ivannguyendev/chatim/pkg/redact"
)

var secretFields = map[string]func(Config) slog.Attr{
	"MongoURI":            func(c Config) slog.Attr { return slog.String("mongo_uri", redact.URL(c.MongoURI)) },
	"MongoPassword":       func(c Config) slog.Attr { return slog.Bool("mongo_auth", c.mongoAuth()) },
	"RedisPassword":       func(c Config) slog.Attr { return slog.Bool("redis_auth", c.RedisPassword != "") },
	"RedisDedupePassword": func(c Config) slog.Attr { return slog.Bool("redis_dedupe_auth", c.RedisDedupePassword != "") },
	"NATSURL":             func(c Config) slog.Attr { return slog.String("nats_url", redact.URL(c.NATSURL)) },
}

func (c Config) mongoAuth() bool { return c.MongoUser != "" || uriHasCredentials(c.MongoURI) }

func (c Config) LogValue() slog.Value {
	return slog.GroupValue(c.attrs(reflect.ValueOf(c), "")...)
}

func (c Config) String() string { return c.LogValue().String() }

func uriHasCredentials(raw string) bool {
	_, rest, ok := strings.Cut(raw, "://")
	base, _, _ := strings.Cut(rest, "?")
	return ok && strings.Contains(base, "@")
}

func (c Config) Secrets() []string {
	out := slices.DeleteFunc([]string{c.MongoPassword, c.RedisPassword, c.RedisDedupePassword}, func(s string) bool { return s == "" })
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	return out
}
