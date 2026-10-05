package domain

import "time"

type Kind uint8

const KindText Kind = 1

type Message struct {
	Room      uint64
	Thread    uint64
	Seq       uint64
	Tenant    string
	From      string
	Kind      Kind
	Text      string
	CID       string
	CreatedAt time.Time
	Version   uint32
	Deleted   bool
	EditedAt  time.Time
	Hidden    bool
}
