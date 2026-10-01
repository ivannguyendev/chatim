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
	clean := redactText(msg, rawURL)
	if clean == msg {
		return err
	}
	return &redactedError{msg: clean, err: err}
}

func redactText(text, rawURL string) string {
	if rawURL == "" {
		return text
	}
	text = strings.ReplaceAll(text, rawURL, RedactURL(rawURL))
	for _, s := range urlSecrets(rawURL) {
		text = strings.ReplaceAll(text, s, redacted)
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
		base, query, _ := strings.Cut(rest, "?")
		if at := strings.LastIndex(base, "@"); at >= 0 {
			userinfo := base[:at]
			out = append(out, userinfo)
			if _, pass, ok := strings.Cut(userinfo, ":"); ok {
				out = append(out, pass)
			}
		}
		for pair := range strings.SplitSeq(query, "&") {
			if key, value, ok := strings.Cut(pair, "="); ok && secretKey(key) {
				out = append(out, value)
			}
		}
	}
	return spellings(out)
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
