package store

import "github.com/ivannguyendev/chatim/apps/core/internal/model/domain"

type ReplyMentionFlags uint32

const (
	HasReply ReplyMentionFlags = 1 << iota
	HasMention
)

func ReplyMentionFlagsOf(m domain.Message) ReplyMentionFlags {
	var f ReplyMentionFlags
	if m.ReplyTo != nil {
		f |= HasReply
	}
	if len(m.Mentions) > 0 || m.MentionAll {
		f |= HasMention
	}
	return f
}
