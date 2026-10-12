package store

import (
	"bytes"
	"cmp"
	"time"

	"github.com/ivannguyendev/chatim/pkg/keys"
)

type InteractionScan struct {
	Room     uint64
	Kind     keys.InteractionKind
	From, To time.Time
	After    *Interaction
	Limit    int
}

func ValidateInteractionScan(q InteractionScan) error {
	if err := ValidateInteractionKind(q.Kind); err != nil {
		return err
	}
	if err := ValidateLimit(q.Limit, MaxInteractionScan); err != nil {
		return err
	}
	if q.After != nil && (q.After.Kind != q.Kind || q.After.Key.Room != q.Room) {
		return invalid("interaction cursor")
	}
	return nil
}

func InteractionID(x Interaction) []byte {
	msg := keys.Msg(x.Key.Room, x.Key.Thread, x.Key.Seq)
	if x.Kind == keys.ReplyKind {
		return keys.InteractionReply(msg, x.Reply.Thread, x.Reply.Seq)
	}
	return keys.InteractionUser(msg, x.Kind, x.User)
}

func CompareInteractions(a, b Interaction) int {
	ia, ib := InteractionID(a), InteractionID(b)
	return cmp.Or(a.At.Compare(b.At), cmp.Compare(len(ia), len(ib)), bytes.Compare(ia, ib))
}
