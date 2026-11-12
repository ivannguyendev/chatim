package domain

import "time"

type PairState int32

const (
	PairLive    PairState = 1
	PairDeleted PairState = 2
)

type MessageFile struct {
	MessageKey string
	FileKey    string
	Tenant     string
	Room       string
	Kind       RoomFileKind
	SentAt     time.Time
	State      PairState
	CreatedAt  time.Time
	DeletedAt  time.Time
}
