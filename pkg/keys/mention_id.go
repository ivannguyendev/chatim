package keys

func ValidMentionID(id string) bool {
	if id == "" || len(id) > MaxMentionID {
		return false
	}
	for i := range len(id) {
		if !mentionIDByte(id[i]) {
			return false
		}
	}
	return true
}

func mentionIDByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return c == '_' || c == '-' || c == ':'
	}
}
