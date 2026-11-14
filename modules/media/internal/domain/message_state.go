package domain

import "time"

type MessageState struct {
	Found   bool
	Deleted bool
	Tenant  string
	Keys    []string
	Mimes   map[string]string
	SentAt  time.Time
}
