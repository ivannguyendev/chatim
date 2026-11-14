package domain

import (
	"fmt"
	"time"
)

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

func (a RoomAvatar) Validate() error {
	if a.Room == "" || a.Tenant == "" || a.InfoVer == 0 {
		return fmt.Errorf("%w: a room avatar needs a room, a tenant and an info ver", ErrInvalid)
	}
	return nil
}
