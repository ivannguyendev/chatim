package domain

const MentionAll MentionKind = 3

func ValidMentionLink(t MentionTarget) bool {
	if t.Kind == MentionAll {
		return t.ID == ""
	}
	return validMentionTarget(t)
}
