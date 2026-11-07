package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const mediaGalleryPage = 10

func (r *mediaRun) attachmentURLs(g *gateway, room string, seq uint64) (*chatimv1.GetAttachmentURLsResponse, error) {
	resp, err := viaGateway(g, g.media.GetAttachmentURLs, &chatimv1.GetAttachmentURLsRequest{Messages: []*chatimv1.MessageRef{{RoomId: room, Seq: seq}}})
	if err != nil {
		return nil, fmt.Errorf("GetAttachmentURLs: %w", err)
	}
	return resp, nil
}

func (r *mediaRun) urlCount(g *gateway, who, room string, seq uint64, want int) error {
	resp, err := r.attachmentURLs(g, room, seq)
	if err != nil {
		return err
	}
	if len(resp.GetMessages()) != want {
		return fmt.Errorf("%s got URLs for %d messages of %s seq %d, want %d", who, len(resp.GetMessages()), room, seq, want)
	}
	return nil
}

func (r *mediaRun) messageFiles(g *gateway, room string, seq uint64) (map[string]*chatimv1.FileURL, error) {
	resp, err := r.attachmentURLs(g, room, seq)
	if err != nil {
		return nil, err
	}
	msgs := resp.GetMessages()
	if len(msgs) != 1 || msgs[0].GetRoomId() != room || msgs[0].GetSeq() != seq || !resp.GetExpiresAt().AsTime().After(time.Now()) {
		return nil, fmt.Errorf("URLs %v", resp)
	}
	files := map[string]*chatimv1.FileURL{}
	for _, f := range msgs[0].GetFiles() {
		if f.GetUrl() == "" || f.GetThumbnailUrl() != "" {
			return nil, fmt.Errorf("file %s has url %t and thumbnail_url %t, want a url and no thumbnail", f.GetKey(), f.GetUrl() != "", f.GetThumbnailUrl() != "")
		}
		files[f.GetKey()] = f
	}
	return files, nil
}

func (r *mediaRun) downloads() error {
	files, err := r.messageFiles(r.b.gw, r.room, r.seq)
	if err != nil {
		return err
	}
	if len(files) != 2 || files[r.image.key] == nil || files[r.page.key] == nil {
		return fmt.Errorf("message seq %d gave B %d files, want the PNG and the HTML", r.seq, len(files))
	}
	if err := checkDownload(r.base, r.image, files[r.image.key].GetUrl(), r.image.mime, "inline"); err != nil {
		return err
	}
	return checkDownload(r.base, r.page, files[r.page.key].GetUrl(), "application/octet-stream", "attachment")
}

func checkDownload(ctx context.Context, f *mediaFile, url, wantType, wantDisposition string) error {
	d, err := getSigned(ctx, url)
	if err != nil {
		return fmt.Errorf("download %s: %w", f.name, err)
	}
	sum := sha256.Sum256(d.body)
	switch {
	case d.status != 200:
		return fmt.Errorf("download %s answered %d", f.name, d.status)
	case !bytes.Equal(d.body, f.body):
		return fmt.Errorf("download %s gave %d bytes that differ from the %d uploaded", f.name, len(d.body), len(f.body))
	case "sha256:"+hex.EncodeToString(sum[:]) != f.hash:
		return fmt.Errorf("download %s hashes to %x, want %s", f.name, sum, f.hash)
	case d.header.Get("Content-Type") != wantType:
		return fmt.Errorf("download %s has Content-Type %q, want %q", f.name, d.header.Get("Content-Type"), wantType)
	case !strings.HasPrefix(d.header.Get("Content-Disposition"), wantDisposition):
		return fmt.Errorf("download %s has Content-Disposition %q, want %s", f.name, d.header.Get("Content-Disposition"), wantDisposition)
	}
	return nil
}

func (r *mediaRun) gallery() error {
	a, err := r.client(r.user)
	if err != nil {
		return err
	}
	all, err := r.uploadKeys(a, nil, r.image.key, r.page.key, r.upload.GetKey())
	if err != nil {
		return err
	}
	first := all[0]
	if first.GetExpired() || first.GetUrl() == "" || first.GetThumbnailUrl() != "" || first.GetHash() != r.image.hash ||
		first.GetMime() != r.image.mime || !slices.Equal(first.GetDim(), r.image.dim) {
		return fmt.Errorf("newest upload of A is %v", first)
	}
	if _, err := r.uploadKeys(a, []chatimv1.FileKind{chatimv1.FileKind_FILE_KIND_IMAGE}, r.image.key); err != nil {
		return err
	}
	b, err := r.client(r.other)
	if err != nil {
		return err
	}
	_, err = r.uploadKeys(b, nil, r.notes.key)
	return err
}

func (r *mediaRun) uploadKeys(g *gateway, kinds []chatimv1.FileKind, want ...string) ([]*chatimv1.UploadedFile, error) {
	resp, err := viaGateway(g, g.media.ListUploads, &chatimv1.ListUploadsRequest{Kinds: kinds, PageSize: mediaGalleryPage})
	if err != nil {
		return nil, fmt.Errorf("ListUploads: %w", err)
	}
	got := make([]string, 0, len(resp.GetFiles()))
	for _, f := range resp.GetFiles() {
		got = append(got, f.GetKey())
	}
	if !slices.Equal(got, want) || resp.GetNextBefore() != "" {
		return nil, fmt.Errorf("ListUploads kinds %v listed %v next %q, want %v", kinds, got, resp.GetNextBefore(), want)
	}
	return resp.GetFiles(), nil
}
