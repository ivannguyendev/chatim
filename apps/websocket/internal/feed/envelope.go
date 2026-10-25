package feed

import (
	"slices"
	"strings"
	"time"
)

type Envelope struct {
	Tenant     string
	DataKind   string
	Room       string
	Kind       string
	ID         string
	Seq        uint64
	StoredAt   time.Time
	CatchingUp bool
	Data       []byte
}

type JumpReason int

const (
	JumpLagTooOld JumpReason = iota
	JumpConsumerLost
)

type Jump struct {
	Reason JumpReason
	Seq    uint64
}

type Handler interface {
	Event(Envelope)
	Publish(Envelope)
	Jumped(Jump)
}

const eventTokens = 4

func parseEvent(root, subject string) (Envelope, bool) {
	tokens, ok := tokensAfter(root, subject, eventTokens)
	if !ok {
		return Envelope{}, false
	}
	return Envelope{Tenant: tokens[0], DataKind: tokens[1], Room: tokens[2], Kind: tokens[3]}, true
}

func parsePublish(root, subject string) (Envelope, bool) {
	tokens, ok := tokensAfter(root, subject, 1)
	if !ok {
		return Envelope{}, false
	}
	return Envelope{Tenant: tokens[0]}, true
}

func tokensAfter(root, subject string, want int) ([]string, bool) {
	rest, ok := strings.CutPrefix(subject, root+".")
	if !ok {
		return nil, false
	}
	tokens := strings.Split(rest, ".")
	if len(tokens) != want {
		return nil, false
	}
	if slices.Contains(tokens, "") {
		return nil, false
	}
	return tokens, true
}

func spread(key string, n int) uint64 {
	if n <= 1 {
		return 0
	}
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := range len(key) {
		h ^= uint64(key[i])
		h *= prime
	}
	return h % uint64(n)
}
