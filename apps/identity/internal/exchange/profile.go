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

func newUser(sub trust.Subject, candidate string, at time.Time) store.NewUser {
	name, _ := claimedName(sub)
	picture, _ := claimedPicture(sub)
	return store.NewUser{
		TenantID: sub.Tenant, Realm: sub.Realm, Sub: sub.User, CandidateID: candidate,
		DisplayName: name, AvatarURL: picture, At: at, By: jitActorPrefix + sub.Issuer,
	}
}

func profileUpdate(sub trust.Subject, cached directory.User, at time.Time) (store.ProfileWrite, bool) {
	w := store.ProfileWrite{TenantID: sub.Tenant, UserID: cached.ID, DisplayName: cached.DisplayName, AvatarURL: cached.AvatarURL,
		At: at, By: jitActorPrefix + sub.Issuer}
	if name, ok := claimedName(sub); ok {
		w.DisplayName = name
	}
	if picture, ok := claimedPicture(sub); ok {
		w.AvatarURL = picture
	}
	if w.DisplayName == cached.DisplayName && w.AvatarURL == cached.AvatarURL {
		return store.ProfileWrite{}, false
	}
	w.Seen = &store.SeenProfile{DisplayName: cached.DisplayName, AvatarURL: cached.AvatarURL}
	return w, true
}
