package domain

import "strings"

const (
	FamilyImage = "image"
	FamilyVideo = "video"
)

type Kinds struct {
	Image bool
	Video bool
	Other bool
}

func (k Kinds) All() bool { return k.Image == k.Video && k.Video == k.Other }

func (k Kinds) Match(mime string) bool {
	switch {
	case k.All():
		return true
	case strings.HasPrefix(mime, thumbnailImagePrefix):
		return k.Image
	case strings.HasPrefix(mime, thumbnailVideoPrefix):
		return k.Video
	default:
		return k.Other
	}
}

func (k Kinds) Families() (included, excluded []string) {
	for _, f := range []struct {
		on   bool
		name string
	}{{k.Image, FamilyImage}, {k.Video, FamilyVideo}} {
		if f.on {
			included = append(included, f.name)
		} else {
			excluded = append(excluded, f.name)
		}
	}
	return included, excluded
}
