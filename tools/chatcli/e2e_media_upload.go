package main

import (
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/pkg/core/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (r *mediaRun) start() error {
	g, err := r.client(r.user)
	if err != nil {
		return err
	}
	up, err := viaGateway(g, g.media.StartUpload, &chatimv1.StartUploadRequest{FileName: "e2e.mp4", ContentType: "video/mp4", Size: mediaFileBytes})
	if err != nil {
		return err
	}
	sizes := []int64{16 * mediaMiB, 16 * mediaMiB, 8 * mediaMiB}
	if len(up.GetParts()) != 3 || len(up.GetUrls()) != 3 || up.GetExpiresAt().AsTime().Before(time.Now()) || !strings.HasPrefix(up.GetKey(), r.tenant+"/"+r.user+"/") {
		return fmt.Errorf("started %v", up)
	}
	for i, p := range up.GetParts() {
		if p.GetSize() != sizes[i] {
			return fmt.Errorf("part %d size %d", p.GetPartNumber(), p.GetSize())
		}
	}
	for _, u := range up.GetUrls() {
		r.urls[u.GetPartNumber()] = u.GetUrl()
	}
	r.upload = up
	return nil
}

func (r *mediaRun) putParts() error {
	body := make([]byte, 16*mediaMiB)
	for _, p := range r.upload.GetParts() {
		status, etag, err := putSigned(r.base, r.urls[p.GetPartNumber()], body[:p.GetSize()])
		if err != nil || status != 200 || etag == "" {
			return fmt.Errorf("part %d: status %d etag %q: %w", p.GetPartNumber(), status, etag, err)
		}
		r.etags = append([]*chatimv1.UploadedPart{{PartNumber: p.GetPartNumber(), Etag: etag}}, r.etags...)
	}
	return nil
}

func (r *mediaRun) complete() error {
	g, err := r.client(r.user)
	if err != nil {
		return err
	}
	done, err := viaGateway(g, g.media.CompleteUpload, &chatimv1.CompleteUploadRequest{Key: r.upload.GetKey(), Parts: r.etags})
	if err != nil || done.GetSize() != mediaFileBytes {
		return fmt.Errorf("complete %v: %w", done, err)
	}
	return nil
}

func (r *mediaRun) completeAgain() error { return r.complete() }

func (r *mediaRun) putAfterComplete() error {
	status, _, err := putSigned(r.base, r.urls[1], make([]byte, 16*mediaMiB))
	if err == nil && status < 400 {
		return fmt.Errorf("PUT after complete answered %d", status)
	}
	return nil
}

func (r *mediaRun) otherUser() error {
	g, err := r.client(r.other)
	if err != nil {
		return err
	}
	_, err = viaGateway(g, g.media.GetPartURLs, &chatimv1.GetPartURLsRequest{Key: r.upload.GetKey(), PartNumbers: []int32{1}})
	return wantCode(err, connect.CodeNotFound)
}

func (r *mediaRun) deviceLimit() error {
	g, err := r.client(r.user)
	if err != nil {
		return err
	}
	up, err := viaGateway(g, g.media.StartUpload, &chatimv1.StartUploadRequest{FileName: "e2e-2.mp4", ContentType: "video/mp4", Size: 100 * mediaMiB})
	if err != nil {
		return err
	}
	r.second = up.GetKey()
	if !backoff.Pause(r.base, mediaWindowPause) {
		return r.base.Err()
	}
	if _, err := viaGateway(g, g.media.GetPartURLs, &chatimv1.GetPartURLsRequest{Key: r.second, PartNumbers: []int32{1}}); err != nil {
		return err
	}
	_, err = viaGateway(g, g.media.GetPartURLs, &chatimv1.GetPartURLsRequest{Key: r.second, PartNumbers: []int32{2}})
	return wantCode(err, connect.CodeResourceExhausted)
}

func (r *mediaRun) tooMany() error {
	if !backoff.Pause(r.base, mediaWindowPause) {
		return r.base.Err()
	}
	g, err := r.client(r.user)
	if err != nil {
		return err
	}
	_, err = viaGateway(g, g.media.GetPartURLs, &chatimv1.GetPartURLsRequest{Key: r.second, PartNumbers: []int32{1, 2, 3, 4, 1}})
	return wantCode(err, connect.CodeInvalidArgument)
}

func (r *mediaRun) abort() error {
	g, err := r.client(r.user)
	if err != nil {
		return err
	}
	if _, err := viaGateway(g, g.media.AbortUpload, &chatimv1.AbortUploadRequest{Key: r.second}); err != nil {
		return err
	}
	if !backoff.Pause(r.base, mediaWindowPause) {
		return r.base.Err()
	}
	_, err = viaGateway(g, g.media.GetPartURLs, &chatimv1.GetPartURLsRequest{Key: r.second, PartNumbers: []int32{1}})
	return wantCode(err, connect.CodeFailedPrecondition)
}
