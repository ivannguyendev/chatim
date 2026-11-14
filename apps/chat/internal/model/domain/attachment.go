package domain

import (
	"mime"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxAttachmentNameBytes = 255
	MaxAttachmentMimeBytes = 255
	attachmentHashPrefix   = "sha256:"
	attachmentHashHex      = 64
)

type Attachment struct {
	Key  string
	Name string
	Mime string
	Size int64
	Dim  []int32
	Dur  int64
	Hash string
}

func ValidateBody(text string, attachments int) error {
	if text == "" && attachments > 0 {
		return nil
	}
	return ValidateText(text)
}

func ValidateAttachments(list []Attachment, tenant, sender string, now time.Time, forwarded bool) ([]Attachment, error) {
	if len(list) == 0 {
		return nil, nil
	}
	if len(list) > MaxAttachments {
		return nil, invalid("attachments")
	}
	out := make([]Attachment, len(list))
	seen := make(map[string]struct{}, len(list))
	for i, a := range list {
		if err := checkAttachmentKey(a.Key, tenant, sender, now, forwarded); err != nil {
			return nil, err
		}
		if _, dup := seen[a.Key]; dup {
			return nil, invalid("attachments.key")
		}
		seen[a.Key] = struct{}{}
		if err := checkAttachmentFacts(a); err != nil {
			return nil, err
		}
		a.Mime = strings.ToLower(a.Mime)
		a.Dim = cloneDim(a.Dim)
		out[i] = a
	}
	return out, nil
}

func checkAttachmentKey(key, tenant, sender string, now time.Time, forwarded bool) error {
	k, ok := parseFileKey(key)
	switch {
	case !ok || k.public || k.tenant != tenant:
		return invalid("attachments.key")
	case forwarded:
		if ValidUser(k.user) != nil {
			return invalid("attachments.key")
		}
		return nil
	case k.user != sender, !k.freshAt(now):
		return invalid("attachments.key")
	}
	return nil
}

func checkAttachmentFacts(a Attachment) error {
	switch {
	case a.Name == "" || len(a.Name) > maxAttachmentNameBytes || !utf8.ValidString(a.Name) || strings.ContainsFunc(a.Name, hiddenNameRune):
		return invalid("attachments.name")
	case len(a.Mime) > MaxAttachmentMimeBytes || !isMediaType(a.Mime):
		return invalid("attachments.mime")
	case a.Size < 1:
		return invalid("attachments.size")
	case !validDim(a.Dim):
		return invalid("attachments.dim")
	case a.Dur < 0:
		return invalid("attachments.dur")
	case !validHash(a.Hash):
		return invalid("attachments.hash")
	}
	return nil
}

func isMediaType(v string) bool {
	parsed, _, err := mime.ParseMediaType(v)
	if err != nil {
		return false
	}
	kind, sub, ok := strings.Cut(parsed, "/")
	return ok && kind != "" && sub != ""
}

func validDim(dim []int32) bool {
	return len(dim) == 0 || (len(dim) == 2 && dim[0] >= 1 && dim[1] >= 1)
}

func validHash(hash string) bool {
	if hash == "" {
		return true
	}
	hex, ok := strings.CutPrefix(hash, attachmentHashPrefix)
	if !ok || len(hex) != attachmentHashHex {
		return false
	}
	for i := range len(hex) {
		c := hex[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func hiddenNameRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}

func cloneDim(dim []int32) []int32 {
	if len(dim) == 0 {
		return nil
	}
	return slices.Clone(dim)
}
