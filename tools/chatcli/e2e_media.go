package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/pkg/core/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/chatcli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

const (
	mediaMiB         = int64(1) << 20
	mediaFileBytes   = 40 * mediaMiB
	mediaReadyWait   = 45 * time.Second
	mediaWindowPause = 1100 * time.Millisecond
)

type mediaRun struct {
	base       context.Context
	key        devtoken.KeyPair
	gatewayURL string
	tenant     string
	user       string
	other      string
	timeout    time.Duration
	tokens     []string
	upload     *chatimv1.StartUploadResponse
	urls       map[int32]string
	etags      []*chatimv1.UploadedPart
	second     string
}

func e2eMedia(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e media", flag.ContinueOnError)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	gatewayURL := fs.String("gateway", defaultGatewayURL, "HTTPS gateway endpoint")
	keyFile := fs.String("key", "/keys/"+devtoken.PrivateFile, "private JWK written by dev-keys")
	callTimeout := fs.Duration("call-timeout", 30*time.Second, "timeout of one request")
	if err := fs.Parse(args); err != nil {
		return err
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	key, err := devtoken.LoadPrivateFile(*keyFile)
	if err != nil {
		return err
	}
	r := &mediaRun{base: ctx, key: key, gatewayURL: strings.TrimRight(*gatewayURL, "/"), tenant: "e2emedia" + suffix, timeout: *callTimeout,
		user: "e2e-media-a", other: "e2e-media-b", urls: map[int32]string{}}
	steps := []wsEventsStep{
		{"GetClientConfig reports media on with 4 parallel uploads, 10 attachments and 4 GiB files", r.config},
		{"StartUpload of 40 MiB plans parts of 16, 16 and 8 MiB with a URL for each", r.start},
		{"the three parts are PUT straight to S3 and give ETags", r.putParts},
		{"CompleteUpload with the ETags in any order marks the file uploaded at 40 MiB", r.complete},
		{"CompleteUpload again answers the same file", r.completeAgain},
		{"a part URL no longer works once the upload is complete", r.putAfterComplete},
		{"another user asking part URLs of the file gets not_found", r.otherUser},
		{"a second GetPartURLs from the same device right away is refused", r.deviceLimit},
		{"asking more part URLs than parallel uploads is invalid", r.tooMany},
		{"after AbortUpload the session gives no more part URLs", r.abort},
	}
	for i, step := range steps {
		if err := step.run(); err != nil {
			return fmt.Errorf("media step %d (%s): %w", i+1, step.name, err)
		}
		fmt.Fprintf(os.Stderr, "media step %d ok: %s\n", i+1, step.name)
	}
	return e2e.WriteTokens(filepath.Join(*dir, "media-tokens"), r.tokens...)
}

func (r *mediaRun) client(user string) (*gateway, error) {
	token, err := devSign(r.key, r.tenant, user, time.Now(), devTokenTTL)
	if err != nil {
		return nil, err
	}
	r.tokens = append(r.tokens, token)
	return newGateway(r.base, r.gatewayURL, token, r.timeout), nil
}

func (r *mediaRun) config() error {
	g, err := r.client(r.user)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(mediaReadyWait)
	for {
		c, err := viaGateway(g, g.chat.GetClientConfig, &chatimv1.GetClientConfigRequest{})
		if err != nil {
			return err
		}
		m := c.GetMedia()
		if m.GetEnabled() {
			if m.GetMaxParallelUploads() != 4 || m.GetMaxFileBytes() != 4<<30 || c.GetMessage().GetMaxAttachments() != 10 {
				return fmt.Errorf("client config %v", c)
			}
			return nil
		}
		if time.Now().After(deadline) || !backoff.Pause(r.base, time.Second) {
			return fmt.Errorf("media not enabled after %s: %v", mediaReadyWait, c)
		}
	}
}

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

func wantCode(err error, want connect.Code) error {
	if got := connect.CodeOf(err); err == nil || got != want {
		return errors.Join(fmt.Errorf("want %s, got %v", want, got), err)
	}
	return nil
}

func randomSuffix() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
