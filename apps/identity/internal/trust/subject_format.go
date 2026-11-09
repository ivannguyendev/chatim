package trust

const maxSubjectLen = 255

func validSubject(sub string) bool {
	if sub == "" || len(sub) > maxSubjectLen {
		return false
	}
	for i := range len(sub) {
		if sub[i] < '!' || sub[i] > '~' {
			return false
		}
	}
	return true
}
