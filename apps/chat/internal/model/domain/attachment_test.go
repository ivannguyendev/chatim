package domain_test

import (
	"encoding/base32"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
)

var (
	keyNow      = time.Date(2026, 11, 7, 10, 30, 0, 0, time.UTC)
	testKeyCode = base32.NewEncoding("0123456789abcdefghijklmnopqrstuv").WithPadding(base32.NoPadding)
)

func keyAt(tenant, user string, at time.Time, seed byte) string {
	return keyDated(tenant, user, at, at.UTC().Format("20060102"), seed)
}

func keyDated(tenant, user string, at time.Time, date string, seed byte) string {
	var raw [16]byte
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(at.UnixMilli()))
	copy(raw[:6], ms[2:])
	for i := 6; i < 16; i++ {
		raw[i] = seed + byte(i)
	}
	return tenant + "/" + user + "/" + date + "/" + testKeyCode.EncodeToString(raw[:])
}

func photo(key string) domain.Attachment {
	return domain.Attachment{Key: key, Name: "ảnh.png", Mime: "image/png", Size: 2048, Dim: []int32{640, 480}, Dur: 0, Hash: "sha256:" + strings.Repeat("ab", 32)}
}

func fresh(seed byte) string {
	return keyAt("acme", "lan", keyNow.Add(-time.Minute), seed)
}

func TestValidateAttachmentsAcceptsOwnFreshFiles(t *testing.T) {
	list := []domain.Attachment{photo(fresh(1)), {Key: fresh(2), Name: "clip.mp4", Mime: "video/mp4", Size: 1, Dur: 12000}}
	got, err := domain.ValidateAttachments(list, "acme", "lan", keyNow, false)
	if err != nil {
		t.Fatalf("ValidateAttachments = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, list) {
		t.Fatalf("got %+v, want %+v", got, list)
	}
	if got, err := domain.ValidateAttachments(nil, "acme", "lan", keyNow, false); err != nil || got != nil {
		t.Fatalf("empty = %v, %v, want nil, nil", got, err)
	}
}

func TestValidateAttachmentsNormalizesMimeWithoutTouchingInput(t *testing.T) {
	in := photo(fresh(1))
	in.Mime = "Image/PNG"
	list := []domain.Attachment{in}
	got, err := domain.ValidateAttachments(list, "acme", "lan", keyNow, false)
	if err != nil {
		t.Fatalf("ValidateAttachments = %v, want nil", err)
	}
	if got[0].Mime != "image/png" || list[0].Mime != "Image/PNG" {
		t.Fatalf("mime = %q, input = %q, want image/png and input kept", got[0].Mime, list[0].Mime)
	}
}

func TestValidateAttachmentsKeyAge(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		ok   bool
	}{
		{"exactly 24h old", keyNow.Add(-24 * time.Hour), true},
		{"23h old", keyNow.Add(-23 * time.Hour), true},
		{"24h and 1ms old", keyNow.Add(-24*time.Hour - time.Millisecond), false},
		{"5m ahead", keyNow.Add(5 * time.Minute), true},
		{"6m ahead", keyNow.Add(6 * time.Minute), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.ValidateAttachments([]domain.Attachment{photo(keyAt("acme", "lan", tt.at, 1))}, "acme", "lan", keyNow, false)
			if tt.ok && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if !tt.ok {
				assertInvalid(t, err, "attachments.key")
			}
		})
	}
}

func TestValidateAttachmentsRejectsBadKeys(t *testing.T) {
	at := keyNow.Add(-time.Minute)
	own := fresh(1)
	random := own[strings.LastIndex(own, "/")+1:]
	tests := map[string]string{
		"other user":          keyAt("acme", "minh", at, 1),
		"other tenant":        keyAt("other", "lan", at, 1),
		"date not key time":   keyDated("acme", "lan", at, at.AddDate(0, 0, -1).Format("20060102"), 1),
		"bad date":            keyDated("acme", "lan", at, "20261340", 1),
		"thumb suffix":        own + ".thumb",
		"uppercase random":    "acme/lan/" + at.Format("20060102") + "/" + strings.ToUpper(random),
		"out of alphabet":     "acme/lan/" + at.Format("20060102") + "/" + "w" + random[1:],
		"short random":        "acme/lan/" + at.Format("20060102") + "/" + random[1:],
		"extra segment":       "acme/lan/x/" + at.Format("20060102") + "/" + random,
		"missing segment":     "acme/" + at.Format("20060102") + "/" + random,
		"empty":               "",
		"over 512 bytes":      "acme/lan/" + strings.Repeat("a", 512),
		"nested user segment": "acme/lan/lan/" + random,
	}
	for name, key := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := domain.ValidateAttachments([]domain.Attachment{photo(key)}, "acme", "lan", keyNow, false)
			assertInvalid(t, err, "attachments.key")
		})
	}
}

func TestValidateAttachmentsForwardedChecksOnlyShape(t *testing.T) {
	old := keyNow.AddDate(0, -2, 0)
	for name, key := range map[string]string{
		"old own key":   keyAt("acme", "lan", old, 1),
		"other user":    keyAt("acme", "Minh-2", old, 2),
		"fresh foreign": keyAt("acme", "minh", keyNow.Add(-time.Minute), 3),
		"date mismatch": keyDated("acme", "minh", old, "20200101", 4),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := domain.ValidateAttachments([]domain.Attachment{photo(key)}, "acme", "lan", keyNow, true); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
	for name, key := range map[string]string{
		"other tenant":   keyAt("other", "minh", old, 1),
		"bad user":       keyAt("acme", "mi.nh", old, 1),
		"thumb suffix":   keyAt("acme", "minh", old, 1) + ".thumb",
		"bad date shape": keyDated("acme", "minh", old, "2026111", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := domain.ValidateAttachments([]domain.Attachment{photo(key)}, "acme", "lan", keyNow, true)
			assertInvalid(t, err, "attachments.key")
		})
	}
}

func TestValidateAttachmentsRejectsBadFields(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*domain.Attachment)
		field string
	}{
		{"empty name", func(a *domain.Attachment) { a.Name = "" }, "attachments.name"},
		{"name 256 bytes", func(a *domain.Attachment) { a.Name = strings.Repeat("a", 256) }, "attachments.name"},
		{"name invalid utf-8", func(a *domain.Attachment) { a.Name = "a\xff" }, "attachments.name"},
		{"name control char", func(a *domain.Attachment) { a.Name = "a\nb" }, "attachments.name"},
		{"mime without subtype", func(a *domain.Attachment) { a.Mime = "image" }, "attachments.mime"},
		{"mime empty", func(a *domain.Attachment) { a.Mime = "" }, "attachments.mime"},
		{"mime over 255", func(a *domain.Attachment) { a.Mime = "image/" + strings.Repeat("p", 250) }, "attachments.mime"},
		{"size 0", func(a *domain.Attachment) { a.Size = 0 }, "attachments.size"},
		{"size negative", func(a *domain.Attachment) { a.Size = -1 }, "attachments.size"},
		{"dim one value", func(a *domain.Attachment) { a.Dim = []int32{640} }, "attachments.dim"},
		{"dim three values", func(a *domain.Attachment) { a.Dim = []int32{1, 2, 3} }, "attachments.dim"},
		{"dim zero", func(a *domain.Attachment) { a.Dim = []int32{640, 0} }, "attachments.dim"},
		{"dur negative", func(a *domain.Attachment) { a.Dur = -1 }, "attachments.dur"},
		{"hash uppercase", func(a *domain.Attachment) { a.Hash = "sha256:" + strings.Repeat("AB", 32) }, "attachments.hash"},
		{"hash short", func(a *domain.Attachment) { a.Hash = "sha256:" + strings.Repeat("ab", 31) }, "attachments.hash"},
		{"hash other algorithm", func(a *domain.Attachment) { a.Hash = "md5:" + strings.Repeat("ab", 32) }, "attachments.hash"},
		{"hash not hex", func(a *domain.Attachment) { a.Hash = "sha256:" + strings.Repeat("zz", 32) }, "attachments.hash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := photo(fresh(1))
			tt.edit(&a)
			_, err := domain.ValidateAttachments([]domain.Attachment{a}, "acme", "lan", keyNow, false)
			assertInvalid(t, err, tt.field)
		})
	}
}

func TestValidateAttachmentsAcceptsOptionalFieldsEmpty(t *testing.T) {
	a := domain.Attachment{Key: fresh(1), Name: strings.Repeat("a", 255), Mime: "application/pdf; charset=binary", Size: 1}
	if _, err := domain.ValidateAttachments([]domain.Attachment{a}, "acme", "lan", keyNow, false); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestValidateAttachmentsRejectsDuplicatesAndTooMany(t *testing.T) {
	_, err := domain.ValidateAttachments([]domain.Attachment{photo(fresh(1)), photo(fresh(1))}, "acme", "lan", keyNow, false)
	assertInvalid(t, err, "attachments.key")
	list := make([]domain.Attachment, 0, domain.MaxAttachments+1)
	for i := range domain.MaxAttachments {
		list = append(list, photo(fresh(byte(i*16))))
	}
	if _, err := domain.ValidateAttachments(list, "acme", "lan", keyNow, false); err != nil {
		t.Fatalf("%d attachments = %v, want nil", len(list), err)
	}
	list = append(list, photo(fresh(200)))
	_, err = domain.ValidateAttachments(list, "acme", "lan", keyNow, false)
	assertInvalid(t, err, "attachments")
}

func TestValidateBody(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		attachments int
		ok          bool
	}{
		{"text only", "hi", 0, true},
		{"text and file", "hi", 1, true},
		{"empty with file", "", 1, true},
		{"empty with many files", "", 10, true},
		{"empty without file", "", 0, false},
		{"spaces without file", "   ", 0, false},
		{"spaces with file", " \n ", 1, false},
		{"too long with file", strings.Repeat("a", domain.MaxTextBytes+1), 1, false},
		{"invalid utf-8 with file", "a\xff", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateBody(tt.text, tt.attachments)
			if tt.ok && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if !tt.ok {
				assertInvalid(t, err, "text")
			}
		})
	}
}
