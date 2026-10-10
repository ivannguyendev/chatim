package domain

type MentionKind uint8

const (
	MentionUser  MentionKind = 1
	MentionGroup MentionKind = 2
)

const DefaultMentionTargetsMax = 50

type MentionTarget struct {
	Kind MentionKind
	ID   string
}

func ValidateMentions(targets []MentionTarget, limit int) ([]MentionTarget, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	seen := make(map[MentionTarget]struct{}, len(targets))
	out := make([]MentionTarget, 0, len(targets))
	for _, t := range targets {
		if !validMentionTarget(t) {
			return nil, invalid("mentions")
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) > limit {
		return nil, invalid("mentions")
	}
	return out, nil
}

func validMentionTarget(t MentionTarget) bool {
	if (t.Kind != MentionUser && t.Kind != MentionGroup) || t.ID == "" || len(t.ID) > maxIDLen {
		return false
	}
	for i := range len(t.ID) {
		if !identByte(t.ID[i], true) && t.ID[i] != ':' {
			return false
		}
	}
	return true
}
