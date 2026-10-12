package keys

import "errors"

type InteractionKind byte

const (
	ReactionKind InteractionKind = 1
	BookmarkKind InteractionKind = 2
	ReplyKind    InteractionKind = 3
)

const (
	InteractionHeadLen  = MsgLen + 1
	replyPartLen        = 16
	InteractionReplyLen = InteractionHeadLen + replyPartLen
	MaxInteractionUser  = 64
)

var ErrKind = errors.New("keys: unknown key kind")

func Interaction(msgKey []byte, kind InteractionKind, part []byte) []byte {
	return append(withKind(msgKey, byte(kind), len(part)), part...)
}

func InteractionUser(msgKey []byte, kind InteractionKind, user string) []byte {
	return append(withKind(msgKey, byte(kind), len(user)), user...)
}

func InteractionReply(msgKey []byte, thread, seq uint64) []byte {
	b := withKind(msgKey, byte(ReplyKind), replyPartLen)
	b = be.AppendUint64(b, thread)
	return be.AppendUint64(b, seq)
}

func InteractionPrefix(msgKey []byte, kind InteractionKind) []byte {
	return withKind(msgKey, byte(kind), 0)
}

func InteractionReplyRange(msgKey []byte) (lo, hi []byte) {
	return InteractionReply(msgKey, 0, 0), Interaction(msgKey, ReplyKind+1, make([]byte, replyPartLen))
}

func ParseInteraction(b []byte) (msgKey []byte, kind InteractionKind, part []byte, err error) {
	if len(b) <= InteractionHeadLen {
		return nil, 0, nil, ErrLength
	}
	kind, part = InteractionKind(b[MsgLen]), b[InteractionHeadLen:]
	switch kind {
	case ReactionKind, BookmarkKind:
		if len(part) > MaxInteractionUser {
			return nil, 0, nil, ErrLength
		}
	case ReplyKind:
		if len(part) != replyPartLen {
			return nil, 0, nil, ErrLength
		}
	default:
		return nil, 0, nil, ErrKind
	}
	return b[:MsgLen:MsgLen], kind, part, nil
}

func ParseReplyPart(part []byte) (thread, seq uint64, err error) {
	if len(part) != replyPartLen {
		return 0, 0, ErrLength
	}
	return be.Uint64(part[:8]), be.Uint64(part[8:]), nil
}

func withKind(msgKey []byte, kind byte, partLen int) []byte {
	b := make([]byte, 0, len(msgKey)+1+partLen)
	b = append(b, msgKey...)
	return append(b, kind)
}
