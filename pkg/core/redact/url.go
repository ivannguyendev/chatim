package redact

import "strings"

const masked = "xxxxx"

var secretQueryKeys = []string{"pass", "secret", "token", "credential", "authmechanismproperties"}

func URL(raw string) string {
	if raw == "" {
		return ""
	}
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return masked
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = masked + "@" + rest[at+1:]
	}
	if base, query, ok := strings.Cut(rest, "?"); ok {
		rest = base + "?" + redactQuery(query)
	}
	return scheme + "://" + rest
}

func redactQuery(query string) string {
	pairs := strings.Split(query, "&")
	for i, pair := range pairs {
		key, _, _ := strings.Cut(pair, "=")
		if secretKey(key) {
			pairs[i] = key + "=" + masked
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
