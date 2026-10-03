package main

import (
	"time"

	"github.com/ivannguyendev/chatim/pkg/keys"
)

type message struct {
	ID   []byte    `bson:"_id"`
	From string    `bson:"f"`
	Pts  int64     `bson:"p"`
	Kind int32     `bson:"kind"`
	Text string    `bson:"text"`
	TS   time.Time `bson:"ts"`
}

func newMessage(room, seq uint64, from, text string) message {
	return message{ID: keys.Msg(room, 0, seq), From: from, Pts: int64(seq), Kind: 1, Text: text, TS: time.Now()}
}
