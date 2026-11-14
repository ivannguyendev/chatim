package domain

import "time"

type RoomAvatarState struct {
	Found   bool
	Tenant  string
	Group   bool
	Keys    []string
	InfoVer uint64
}

type RoomAvatar struct {
	Room      string
	Tenant    string
	FileKey   string
	InfoVer   uint64
	UpdatedAt time.Time
}
