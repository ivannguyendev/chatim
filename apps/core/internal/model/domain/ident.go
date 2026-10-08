package domain

const (
	maxTenantLen = 32
	maxIDLen     = 64
)

func ValidTenant(s string) error { return checkIdent(s, maxTenantLen, false, "tenant") }

func ValidUser(s string) error { return checkIdent(s, maxIDLen, true, "user") }

func ValidCID(s string) error { return checkIdent(s, maxIDLen, true, "cid") }

func checkIdent(s string, maxLen int, allowUpper bool, field string) error {
	if s == "" || len(s) > maxLen {
		return invalid(field)
	}
	for i := range len(s) {
		if !identByte(s[i], allowUpper) {
			return invalid(field)
		}
	}
	return nil
}

func identByte(c byte, allowUpper bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
		return true
	case c >= 'A' && c <= 'Z':
		return allowUpper
	default:
		return false
	}
}
