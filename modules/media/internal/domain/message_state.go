package domain

type MessageState struct {
	Found   bool
	Deleted bool
	Tenant  string
	Keys    []string
}
