package config

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
)

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.err }

func RedactError(err error, rawURL string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	clean := RedactText(msg, rawURL)
	if clean == msg {
		return err
	}
	return &redactedError{msg: clean, err: err}
}

func RedactText(text string, rawURLs ...string) string {
	for _, raw := range rawURLs {
		if raw == "" {
			continue
		}
		text = strings.ReplaceAll(text, raw, RedactURL(raw))
		for _, s := range urlSecrets(raw) {
			text = strings.ReplaceAll(text, s, redacted)
		}
	}
	return text
}

func urlSecrets(rawURL string) []string {
	var out []string
	for piece := range strings.SplitSeq(rawURL, ",") {
		_, rest, ok := strings.Cut(piece, "://")
		if !ok {
			continue
		}
		base, _, _ := strings.Cut(rest, "?")
		if at := strings.LastIndex(base, "@"); at >= 0 {
			userinfo := base[:at]
			out = append(out, userinfo)
			if _, pass, ok := strings.Cut(userinfo, ":"); ok {
				out = append(out, pass)
			}
		}
	}
	if _, query, ok := strings.Cut(rawURL, "?"); ok {
		out = append(out, querySecrets(query)...)
	}
	return spellings(out)
}

func querySecrets(query string) []string {
	var out []string
	for pair := range strings.SplitSeq(query, "&") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || !secretKey(key) {
			continue
		}
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			decoded = value
		}
		out = append(out, value)
		for item := range strings.SplitSeq(decoded, ",") {
			if _, v, ok := strings.Cut(item, ":"); ok {
				out = append(out, v)
			}
		}
	}
	return out
}

func spellings(secrets []string) []string {
	var out []string
	for _, s := range secrets {
		out = append(out, s)
		if u, err := url.PathUnescape(s); err == nil {
			out = append(out, u)
		}
		if u, err := url.QueryUnescape(s); err == nil {
			out = append(out, u)
		}
	}
	out = slices.DeleteFunc(out, func(s string) bool { return s == "" })
	slices.SortFunc(out, func(a, b string) int { return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b)) })
	return slices.Compact(out)
}
