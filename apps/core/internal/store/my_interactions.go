package store

import "github.com/ivannguyendev/chatim/apps/core/internal/model/domain"

type MyInteraction struct {
	Reaction   string
	Bookmarked bool
}

func ValidateMyInteractions(user string, keys []MsgKey) error {
	if err := domain.ValidUser(user); err != nil {
		return err
	}
	for _, k := range keys {
		if err := k.Validate(); err != nil {
			return err
		}
	}
	return nil
}
