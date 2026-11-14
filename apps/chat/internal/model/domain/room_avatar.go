package domain

import "time"

const MaxRoomAvatars = 1

func ValidateAvatarKeys(keys []string, tenant, caller string, now time.Time) error {
	for _, key := range keys {
		k, ok := parseFileKey(key)
		if !ok || !k.public || k.tenant != tenant || k.user != caller || !k.freshAt(now) {
			return invalid("avatars")
		}
	}
	return nil
}
