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
	base        context.Context
	key         devtoken.KeyPair
	gatewayURL  string
	wsURL       string
	tenant      string
	user        string
	other       string
	third       string
	timeout     time.Duration
	wait        time.Duration
	tokens      []string
	upload      *chatimv1.StartUploadResponse
	urls        map[int32]string
	etags       []*chatimv1.UploadedPart
	second      string
	a, b        *wsPeer
	image       *mediaFile
	page        *mediaFile
	notes       *mediaFile
	room        string
	seq         uint64
	forwardRoom string
	forwardSeq  uint64
}

func e2eMedia(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e media", flag.ContinueOnError)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	gatewayURL := fs.String("gateway", defaultGatewayURL, "HTTPS gateway endpoint")
	wsURL := fs.String("url", "ws://chatim-websocket-1:8080/ws", "websocket endpoint")
	keyFile := fs.String("key", "/keys/"+devtoken.PrivateFile, "private JWK written by dev-keys")
	callTimeout := fs.Duration("call-timeout", 30*time.Second, "timeout of one request")
	wait := fs.Duration("wait", 30*time.Second, "how long to wait for one expected websocket frame")
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
	r := &mediaRun{base: ctx, key: key, gatewayURL: strings.TrimRight(*gatewayURL, "/"), wsURL: *wsURL, tenant: "e2emedia" + suffix,
		timeout: *callTimeout, wait: *wait, user: "e2e-media-a", other: "e2e-media-b", third: "e2e-media-c", urls: map[int32]string{}}
	r.a = &wsPeer{name: "A", user: r.user, base: ctx, timeout: r.timeout, wait: r.wait}
	r.b = &wsPeer{name: "B", user: r.other, base: ctx, timeout: r.timeout, wait: r.wait}
	defer r.a.close()
	defer r.b.close()
	for i, step := range r.steps() {
		if err := step.run(); err != nil {
			return fmt.Errorf("media step %d (%s): %w", i+1, step.name, err)
		}
		fmt.Fprintf(os.Stderr, "media step %d ok: %s\n", i+1, step.name)
	}
	return e2e.WriteTokens(filepath.Join(*dir, "media-tokens"), r.tokens...)
}

func (r *mediaRun) steps() []wsEventsStep {
	return []wsEventsStep{
		{"GetClientConfig reports media on with 4 parallel uploads, 10 attachments, 4 GiB files and no thumbnails", r.config},
		{"StartUpload of 40 MiB plans parts of 16, 16 and 8 MiB with a URL for each", r.start},
		{"the three parts are PUT straight to S3 and give ETags", r.putParts},
		{"CompleteUpload with the ETags in any order marks the file uploaded at 40 MiB", r.complete},
		{"CompleteUpload again answers the same file", r.completeAgain},
		{"a part URL no longer works once the upload is complete", r.putAfterComplete},
		{"another user asking part URLs of the file gets not_found", r.otherUser},
		{"a second GetPartURLs from the same device right away is refused", r.deviceLimit},
		{"asking more part URLs than parallel uploads is invalid", r.tooMany},
		{"after AbortUpload the session gives no more part URLs", r.abort},
		{"A uploads an HTML page then a PNG with dim and hash, B uploads a text file", r.uploadFiles},
		{"A and B connect to the websocket and A creates a group with B", r.connect},
		{"A sends a message with no text and both files over the websocket", r.sendFiles},
		{"B reads the message through the gateway with both attachments", r.readFiles},
		{"B's room list preview names both file mimes", r.previewMimes},
		{"B's plain URLs give the PNG and the HTML with their exact bytes and hashes and no thumbnail", r.downloads},
		{"ListUploads gives A the PNG first then the HTML and the 40 MiB file, kind image only the PNG, and B only its own file", r.gallery},
		{"after B hides the message B gets no URL for it while A still does", r.hidden},
		{"A forwards the message to a group with C and deletes the original; the original has no URL and C downloads through the forward", r.forwarded},
	}
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
			if m.GetMaxParallelUploads() != 4 || m.GetMaxFileBytes() != 4<<30 || c.GetMessage().GetMaxAttachments() != 10 || m.GetThumbnails() {
				return fmt.Errorf("client config %v", c)
			}
			return nil
		}
		if time.Now().After(deadline) || !backoff.Pause(r.base, time.Second) {
			return fmt.Errorf("media not enabled after %s: %v", mediaReadyWait, c)
		}
	}
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
