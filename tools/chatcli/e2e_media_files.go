package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"slices"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	mediaImageWidth  = 3
	mediaImageHeight = 2
	mediaPageBody    = "<!doctype html><html><body><script>alert(document.cookie)</script></body></html>"
)

type mediaFile struct {
	owner  string
	name   string
	mime   string
	body   []byte
	dim    []int32
	hash   string
	key    string
	public bool
}

func newMediaFile(owner, name, mime string, body []byte, dim []int32) *mediaFile {
	sum := sha256.Sum256(body)
	return &mediaFile{owner: owner, name: name, mime: mime, body: body, dim: dim, hash: "sha256:" + hex.EncodeToString(sum[:])}
}

func pngBody() ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, mediaImageWidth, mediaImageHeight))
	img.Set(1, 1, color.RGBA{R: 200, G: 30, B: 90, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (f *mediaFile) attachment() *chatimv1.Attachment {
	return &chatimv1.Attachment{Key: f.key, Name: f.name, Mime: f.mime, Size: int64(len(f.body)), Dim: f.dim, Hash: f.hash}
}

func (f *mediaFile) sameAttachment(a *chatimv1.Attachment) error {
	if a.GetKey() != f.key || a.GetName() != f.name || a.GetMime() != f.mime || a.GetSize() != int64(len(f.body)) ||
		!slices.Equal(a.GetDim(), f.dim) || a.GetHash() != f.hash {
		return fmt.Errorf("attachment %v, want %v", a, f.attachment())
	}
	return nil
}

func (r *mediaRun) uploadFiles() error {
	pic, err := pngBody()
	if err != nil {
		return err
	}
	r.page = newMediaFile(r.user, "e2e page.html", "text/html", []byte(mediaPageBody), nil)
	r.image = newMediaFile(r.user, "e2e.png", "image/png", pic, []int32{mediaImageWidth, mediaImageHeight})
	r.notes = newMediaFile(r.other, "notes.txt", "text/plain", []byte("notes of B"), nil)
	for _, f := range []*mediaFile{r.page, r.image, r.notes} {
		if err := r.uploadSmall(f); err != nil {
			return fmt.Errorf("upload %s: %w", f.name, err)
		}
	}
	return nil
}

func (r *mediaRun) uploadSmall(f *mediaFile) error {
	g, err := r.client(f.owner)
	if err != nil {
		return err
	}
	up, err := viaGateway(g, g.media.StartUpload, &chatimv1.StartUploadRequest{FileName: f.name, ContentType: f.mime, Size: int64(len(f.body)), Dim: f.dim, Public: f.public})
	if err != nil {
		return err
	}
	if len(up.GetUrls()) != 1 || !strings.HasPrefix(up.GetKey(), r.tenant+"/"+f.owner+"/") {
		return fmt.Errorf("started %v", up)
	}
	status, etag, err := putSigned(r.base, up.GetUrls()[0].GetUrl(), f.body)
	if err != nil || status != 200 || etag == "" {
		return fmt.Errorf("put: status %d etag %q: %w", status, etag, err)
	}
	done, err := viaGateway(g, g.media.CompleteUpload, &chatimv1.CompleteUploadRequest{
		Key: up.GetKey(), Parts: []*chatimv1.UploadedPart{{PartNumber: 1, Etag: etag}}, Hash: f.hash,
	})
	if err != nil {
		return err
	}
	if done.GetSize() != int64(len(f.body)) {
		return fmt.Errorf("completed %v", done)
	}
	f.key = up.GetKey()
	return nil
}
