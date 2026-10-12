package keys

type MentionKind byte

const (
	MentionUserKind  MentionKind = 1
	MentionGroupKind MentionKind = 2
	MentionAllKind   MentionKind = 3
)

const (
	MentionHeadLen = MsgLen + 1
	MaxMentionID   = 64
)

func Mention(msgKey []byte, kind MentionKind, id string) []byte {
	return append(withKind(msgKey, byte(kind), len(id)), id...)
}

func ParseMention(b []byte) (msgKey []byte, kind MentionKind, id string, err error) {
	if len(b) < MentionHeadLen {
		return nil, 0, "", ErrLength
	}
	kind, n := MentionKind(b[MsgLen]), len(b)-MentionHeadLen
	switch kind {
	case MentionUserKind, MentionGroupKind:
		if n < 1 || n > MaxMentionID {
			return nil, 0, "", ErrLength
		}
	case MentionAllKind:
		if n != 0 {
			return nil, 0, "", ErrLength
		}
	default:
		return nil, 0, "", ErrKind
	}
	return b[:MsgLen:MsgLen], kind, string(b[MentionHeadLen:]), nil
}
