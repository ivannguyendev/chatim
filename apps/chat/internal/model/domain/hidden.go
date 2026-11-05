package domain

import "time"

type HiddenMessage struct {
	User   string
	Room   uint64
	Thread uint64
	Seq    uint64
	At     time.Time
}
