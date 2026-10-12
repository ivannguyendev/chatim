package store

import (
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func ReactionKeyOf(r domain.Reaction) MsgKey {
	return MsgKey{Room: r.Room, Thread: r.Thread, Seq: r.Seq}
}

func BookmarkKeyOf(b domain.Bookmark) MsgKey {
	return MsgKey{Room: b.Room, Thread: b.Thread, Seq: b.Seq}
}

func ReplyKeyOf(r domain.Reply) MsgKey {
	return MsgKey{Room: r.Room, Thread: r.Thread, Seq: r.Seq}
}

func ValidateReaction(r domain.Reaction) error {
	if err := ValidateReactionTarget(ReactionKeyOf(r), r.User); err != nil {
		return err
	}
	if r.Tenant == "" {
		return invalid("tenant")
	}
	return nil
}

func ValidateReactionTarget(key MsgKey, user string) error {
	if err := key.Validate(); err != nil {
		return err
	}
	return domain.ValidUser(user)
}

func ValidateBookmark(b domain.Bookmark) error {
	if err := ValidateReactionTarget(BookmarkKeyOf(b), b.User); err != nil {
		return err
	}
	if b.Tenant == "" {
		return invalid("tenant")
	}
	return ValidateMarkTime(b.At)
}

func ValidateReply(r domain.Reply) error {
	parent := MsgKey(r.Parent)
	switch {
	case parent.Validate() != nil || parent.Thread != 0:
		return invalid("parent")
	case ReplyKeyOf(r).Validate() != nil || r.Thread != 0:
		return invalid("reply")
	case r.Room != parent.Room:
		return invalid("reply room")
	case r.Seq == parent.Seq:
		return invalid("reply")
	case r.Tenant == "":
		return invalid("tenant")
	case domain.ValidUser(r.From) != nil:
		return invalid("user")
	}
	return ValidateMarkTime(r.At)
}

func ValidateBookmarkQuery(tenant, user string, limit int) error {
	if tenant == "" {
		return invalid("tenant")
	}
	if err := domain.ValidUser(user); err != nil {
		return err
	}
	return ValidateLimit(limit, MaxPageLimit)
}

func ValidateInteractionKind(kind keys.InteractionKind) error {
	switch kind {
	case keys.ReactionKind, keys.BookmarkKind, keys.ReplyKind:
		return nil
	default:
		return invalid("interaction kind")
	}
}
