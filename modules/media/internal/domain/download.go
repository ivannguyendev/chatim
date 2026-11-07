package domain

import (
	"fmt"
	"strings"
)

const (
	dispositionInline    = "inline"
	forcedDownloadType   = "application/octet-stream"
	rfc5987Unreserved    = "!#$&+-.^_`|~"
	fallbackReplacement  = '_'
	fallbackUnsafeASCII  = `"\%`
	firstPrintableASCII  = 0x20
	lastPrintableASCII   = 0x7e
	thumbnailImagePrefix = "image/"
	thumbnailVideoPrefix = "video/"
	percentHexDigits     = "0123456789ABCDEF"
)

var inlineTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "video/mp4": true,
}

type Caller struct{ Tenant, User string }

type MessageRef struct {
	Room   string
	Thread uint64
	Seq    uint64
}

type File struct{ Key, Name, Mime string }

type MessageFiles struct {
	Ref   MessageRef
	Files []File
}

type Disposition struct {
	ContentType string
	Disposition string
}

func FileDisposition(name, mime string) Disposition {
	if inlineTypes[mime] {
		return Disposition{ContentType: mime, Disposition: dispositionInline}
	}
	return Disposition{
		ContentType: forcedDownloadType,
		Disposition: fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, asciiFallback(name), percentEncode(name)),
	}
}

func ThumbnailDisposition() Disposition { return Disposition{Disposition: dispositionInline} }

type Thumbnails struct {
	Enabled bool
	Suffix  string
}

func (t Thumbnails) KeyFor(key, mime string) (string, bool) {
	if !t.Enabled || (!strings.HasPrefix(mime, thumbnailImagePrefix) && !strings.HasPrefix(mime, thumbnailVideoPrefix)) {
		return "", false
	}
	return ThumbnailKey(key, t.Suffix), true
}

func asciiFallback(name string) string {
	return strings.Map(func(r rune) rune {
		if r < firstPrintableASCII || r > lastPrintableASCII || strings.ContainsRune(fallbackUnsafeASCII, r) {
			return fallbackReplacement
		}
		return r
	}, name)
}

func percentEncode(name string) string {
	var b strings.Builder
	for i := range len(name) {
		c := name[i]
		if isAlphaNumeric(c) || strings.IndexByte(rfc5987Unreserved, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(percentHexDigits[c>>4])
		b.WriteByte(percentHexDigits[c&0x0f])
	}
	return b.String()
}

func isAlphaNumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
