package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
)

func TestValidateAvatarKeysAcceptsOwnFreshPublicKeys(t *testing.T) {
	for name, keys := range map[string][]string{
		"none":           nil,
		"own fresh":      {publicKeyAt("acme", "lan", keyNow.Add(-time.Hour), 1)},
		"almost a day":   {publicKeyAt("acme", "lan", keyNow.Add(-24*time.Hour+time.Second), 1)},
		"slightly ahead": {publicKeyAt("acme", "lan", keyNow.Add(4*time.Minute), 1)},
	} {
		if err := domain.ValidateAvatarKeys(keys, "acme", "lan", keyNow); err != nil {
			t.Fatalf("%s: ValidateAvatarKeys = %v, want nil", name, err)
		}
	}
}

func TestValidateAvatarKeysRefusesKeysThatAreNotOwnFreshPublicFiles(t *testing.T) {
	at := keyNow.Add(-time.Minute)
	public := publicKeyAt("acme", "lan", at, 1)
	random := public[strings.LastIndex(public, "/")+1:]
	for name, key := range map[string]string{
		"private":           keyAt("acme", "lan", at, 1),
		"other user":        publicKeyAt("acme", "minh", at, 1),
		"other tenant":      publicKeyAt("other", "lan", at, 1),
		"25 hours old":      publicKeyAt("acme", "lan", keyNow.Add(-25*time.Hour), 1),
		"10 minutes ahead":  publicKeyAt("acme", "lan", keyNow.Add(10*time.Minute), 1),
		"date not key time": "acme/lan/pub/" + at.AddDate(0, 0, -1).Format("20060102") + "/" + random,
		"thumb suffix":      public + ".thumb",
		"pub twice":         "acme/lan/pub/pub/" + at.Format("20060102") + "/" + random,
		"empty":             "",
	} {
		t.Run(name, func(t *testing.T) {
			assertInvalid(t, domain.ValidateAvatarKeys([]string{key}, "acme", "lan", keyNow), "avatars")
		})
	}
}
