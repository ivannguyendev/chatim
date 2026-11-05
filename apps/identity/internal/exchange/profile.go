package exchange

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
)

const jitActorPrefix = "jit:"

func claimedName(sub trust.Subject) (string, bool) {
	if !sub.HasName {
		return "", false
	}
	name, err := directory.NormalizeDisplayName(sub.Name)
	return name, err == nil
}

func claimedPicture(sub trust.Subject) (string, bool) {
	if !sub.HasPicture || sub.Picture == "" || directory.ValidateAvatarURL(sub.Picture) != nil {
		return "", false
	}
	return sub.Picture, true
}

func profileWrite(sub trust.Subject, cached directory.User, known bool, at time.Time) (store.ProfileWrite, bool) {
	w := store.ProfileWrite{TenantID: sub.Tenant, UserID: sub.User, At: at, By: jitActorPrefix + sub.Issuer}
	name, hasName := claimedName(sub)
	picture, hasPicture := claimedPicture(sub)
	if !known {
		w.DisplayName = sub.User
		if hasName {
			w.DisplayName = name
		}
		w.AvatarURL = picture
		return w, true
	}
	w.DisplayName, w.AvatarURL = cached.DisplayName, cached.AvatarURL
	if hasName {
		w.DisplayName = name
	}
	if hasPicture {
		w.AvatarURL = picture
	}
	if w.DisplayName == cached.DisplayName && w.AvatarURL == cached.AvatarURL {
		return store.ProfileWrite{}, false
	}
	w.Seen = &store.SeenProfile{DisplayName: cached.DisplayName, AvatarURL: cached.AvatarURL}
	return w, true
}
