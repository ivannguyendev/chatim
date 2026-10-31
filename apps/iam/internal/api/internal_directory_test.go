package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

func TestFeedReturnsAtOnceWhenThereAreChanges(t *testing.T) {
	f := newFeedHarness(t, time.Minute)
	skipped := store.Change{Kind: store.ChangeUser, TenantID: "shop", ID: "gone", Ver: 1}
	f.feed.push(feedNext, userChange("lan", nil), skipped, userChange("minh", []string{"sales"}), groupChange())
	rec := f.serve("?after="+directory.EncodePosition(feedStart)+"&wait=30", nodeSecret)
	body := decodeFeed(t, rec)
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", rec.Header())
	}
	if len(body.Changes) != 3 || body.Next != directory.EncodePosition(feedNext) {
		t.Fatalf("body = %s; want 3 records and the page cursor", rec.Body)
	}
	userKeys := []string{"avatar_url", "display_name", "groups", "id", "kind", "state", "tenant_id", "ver"}
	if got := keysOf(body.Changes[0]); !slices.Equal(got, userKeys) {
		t.Fatalf("user keys = %v; want %v", got, userKeys)
	}
	if !strings.Contains(rec.Body.String(), `{"kind":"user","tenant_id":"shop","id":"lan","ver":3,"state":"active","display_name":"Lan","avatar_url":"https://cdn/lan.png","groups":[]}`) {
		t.Fatalf("user record = %s", rec.Body)
	}
	if groups := body.Changes[1]["groups"]; !slices.Equal(anyStrings(groups), []string{"sales"}) {
		t.Fatalf("groups = %v", groups)
	}
	if !strings.Contains(rec.Body.String(), `{"kind":"group","tenant_id":"shop","id":"sales","ver":4,"state":"deleted","name":"Sales","parent_id":"","mentionable":true}`) {
		t.Fatalf("group record = %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "email") || strings.Contains(rec.Body.String(), "username") || strings.Contains(rec.Body.String(), "gone") {
		t.Fatalf("body leaks fields or a nil record: %s", rec.Body)
	}
	afters, limits := f.feed.calls()
	if len(afters) != 1 || afters[0] != feedStart || limits[0] != 500 {
		t.Fatalf("store calls after %v limit %v; want one call from the cursor with limit 500", afters, limits)
	}
	if f.stats.OK.Load() != 1 || f.stats.Waiting.Load() != 0 {
		t.Fatalf("ok %d waiting %d", f.stats.OK.Load(), f.stats.Waiting.Load())
	}
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func TestFeedWithoutWaitAnswersEmptyAtOnce(t *testing.T) {
	f := newFeedHarness(t, time.Minute)
	body := decodeFeed(t, f.serve("?limit=1000", nodeSecret))
	if len(body.Changes) != 0 || body.Next != "" {
		t.Fatalf("body = %+v; want no changes and the empty cursor", body)
	}
	if _, limits := f.feed.calls(); !slices.Equal(limits, []int{1000}) || f.stats.Empty.Load() != 1 {
		t.Fatalf("limits %v empty %d", limits, f.stats.Empty.Load())
	}
}

func TestFeedWaitsAndWakesOnSignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFeedHarness(t, time.Minute)
		done := f.start("?after=" + directory.EncodePosition(feedStart) + "&wait=30")
		synctest.Wait()
		requireNotDone(t, done)
		if f.stats.Waiting.Load() != 1 {
			t.Fatalf("waiting = %d; want 1", f.stats.Waiting.Load())
		}
		began := time.Now()
		f.feed.push(feedNext, userChange("lan", nil))
		f.signal.fire()
		body := decodeFeed(t, <-done)
		if elapsed := time.Since(began); elapsed != 0 || len(body.Changes) != 1 || body.Next != directory.EncodePosition(feedNext) {
			t.Fatalf("elapsed %v body %+v; want the change at once", elapsed, body)
		}
		if f.stats.Waiting.Load() != 0 || f.stats.OK.Load() != 1 {
			t.Fatalf("waiting %d ok %d", f.stats.Waiting.Load(), f.stats.OK.Load())
		}
	})
}

func TestFeedRechecksEveryTwoSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFeedHarness(t, time.Minute)
		began := time.Now()
		done := f.start("?wait=30")
		synctest.Wait()
		f.feed.push(feedNext, groupChange())
		body := decodeFeed(t, <-done)
		if elapsed := time.Since(began); elapsed != 2*time.Second || len(body.Changes) != 1 {
			t.Fatalf("elapsed %v body %+v; want the change found by the 2s recheck", elapsed, body)
		}
	})
}

func TestWaitIsCappedAtTheMax(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFeedHarness(t, 5*time.Second)
		began := time.Now()
		rec := <-f.start("?after=" + directory.EncodePosition(feedStart) + "&wait=99999999999999999999")
		body := decodeFeed(t, rec)
		if elapsed := time.Since(began); elapsed != 5*time.Second {
			t.Fatalf("elapsed %v; want the 5s cap", elapsed)
		}
		if len(body.Changes) != 0 || body.Next != directory.EncodePosition(feedStart) {
			t.Fatalf("body = %+v; want empty with the same cursor", body)
		}
		if afters, _ := f.feed.calls(); len(afters) != 3 || f.stats.Empty.Load() != 1 {
			t.Fatalf("store calls %d empty %d; want 3 calls (0s, 2s, 4s) and one empty answer", len(afters), f.stats.Empty.Load())
		}
	})
}

func TestShutdownReleasesWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFeedHarness(t, time.Minute)
		first, second := f.start("?wait=30"), f.start("?after="+directory.EncodePosition(feedStart)+"&wait=30")
		synctest.Wait()
		if f.stats.Waiting.Load() != 2 {
			t.Fatalf("waiting = %d; want 2", f.stats.Waiting.Load())
		}
		began := time.Now()
		f.handler.Stop()
		f.handler.Stop()
		for _, done := range []<-chan *httptest.ResponseRecorder{first, second} {
			if body := decodeFeed(t, <-done); len(body.Changes) != 0 {
				t.Fatalf("body = %+v; want empty", body)
			}
		}
		late := decodeFeed(t, <-f.start("?wait=30"))
		if elapsed := time.Since(began); elapsed != 0 || len(late.Changes) != 0 {
			t.Fatalf("elapsed %v; want every waiter and a late request released at once", elapsed)
		}
		if f.stats.Waiting.Load() != 0 || f.stats.Empty.Load() != 3 {
			t.Fatalf("waiting %d empty %d", f.stats.Waiting.Load(), f.stats.Empty.Load())
		}
	})
}

func TestClientLeavingStopsTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFeedHarness(t, time.Minute)
		ctx, cancel := context.WithCancel(t.Context())
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, feedPath+"?wait=30", nil)
		req.Header.Set("Authorization", "Bearer "+nodeSecret)
		done := make(chan struct{})
		go func() {
			defer close(done)
			f.handler.ServeHTTP(httptest.NewRecorder(), req)
		}()
		synctest.Wait()
		cancel()
		<-done
		if f.stats.Waiting.Load() != 0 {
			t.Fatalf("waiting = %d after the client left", f.stats.Waiting.Load())
		}
	})
}

func TestBadSecretIs401(t *testing.T) {
	f := newFeedHarness(t, time.Minute)
	f.feed.push(feedNext, userChange("lan", nil))
	for _, secret := range []string{"", "wrong", nodeSecret + "x"} {
		if rec := f.serve("", secret); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "lan") {
			t.Fatalf("secret %q = %d %s; want 401", secret, rec.Code, rec.Body)
		}
	}
	if afters, _ := f.feed.calls(); len(afters) != 0 || f.stats.Unauthorized.Load() != 3 {
		t.Fatalf("store calls %d unauthorized %d", len(afters), f.stats.Unauthorized.Load())
	}
}

func TestBadCursorIs400(t *testing.T) {
	f := newFeedHarness(t, time.Minute)
	for _, query := range []string{
		"?after=%%%secretinput", "?after=secretinput!", "?after=" + strings.Repeat("A", 1100),
		"?limit=0", "?limit=1001", "?limit=secretinput", "?limit=-1", "?limit=+5", "?limit=",
		"?wait=-1", "?wait=1.5", "?wait=secretinput", "?wait=+1",
		"?limit=5&limit=6", "?wait=1;secretinput",
	} {
		rec := f.serve(query, nodeSecret)
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusBadRequest || body["error"] == "" || len(body) != 1 {
			t.Fatalf("%s = %d %s; want 400 with one error field", query, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "secretinput") || strings.Contains(rec.Body.String(), "AAAA") {
			t.Fatalf("%s echoed the input: %s", query, rec.Body)
		}
	}
	if afters, _ := f.feed.calls(); len(afters) != 0 || f.stats.BadRequest.Load() != 15 {
		t.Fatalf("store calls %d bad requests %d", len(afters), f.stats.BadRequest.Load())
	}
}

func TestStoreErrorIs503WithRetryAfter(t *testing.T) {
	f := newFeedHarness(t, time.Minute)
	f.feed.err = errors.New("postgres down at 10.0.0.5")
	rec := f.serve("?wait=30", nodeSecret)
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" || body["error"] == "" {
		t.Fatalf("store error = %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if strings.Contains(rec.Body.String(), "postgres") || f.stats.Errors.Load() != 1 {
		t.Fatalf("body %s errors %d", rec.Body, f.stats.Errors.Load())
	}
}

func TestUnmountedFeedIs404(t *testing.T) {
	h := newInternal(t)
	if r := h.get(t, feedPath, bearer(nodeSecret)); r.StatusCode != http.StatusNotFound {
		t.Fatalf("feed without a store = %d; want 404", r.StatusCode)
	}
}

func TestFeedNeedsASignal(t *testing.T) {
	_, err := api.NewInternal(api.InternalOptions{Revocations: &fakeRevocations{}, Secret: nodeSecret, DirectoryFeed: &fakeFeed{}, FeedWaitMax: time.Second})
	if err == nil {
		t.Fatal("accepted a feed without a signal")
	}
}
