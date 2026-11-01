package app_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

func httpPost(t *testing.T, addr, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func TestAdminServiceIsMounted(t *testing.T) {
	kek := newKEK(t)
	r := runIAM(t, testConfig(kek), &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3}, 3)
	r.logs.await(t, "iam ready")
	code, body := httpPost(t, r.api, "/chatim.iam.v1.AdminService/ListRoles", "{}")
	if code != http.StatusUnauthorized || !strings.Contains(body, `"unauthenticated"`) {
		t.Fatalf("ListRoles without a token = %d %s; want 401 unauthenticated", code, body)
	}
	if code, body := httpGet(t, r.admin, "/metrics"); code != http.StatusOK || !strings.Contains(body, `chatim_iam_admin_calls_total{result="unauthenticated"} 1`) {
		t.Fatalf("metrics = %d, the refused admin call is not counted", code)
	}
}

func TestAdminMetricsAreExported(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3}
	st.slotTimeouts.Store(3)
	r := runIAM(t, testConfig(kek), st, 3)
	r.logs.await(t, "iam ready")
	code, body := httpGet(t, r.admin, "/metrics")
	if code != http.StatusOK {
		t.Fatalf("metrics = %d", code)
	}
	for _, series := range []string{
		`chatim_iam_admin_calls_total{result="ok"} 0`,
		`chatim_iam_admin_calls_total{result="denied"} 0`,
		`chatim_iam_admin_denied_total{rpc="ListRoles"} 0`,
		"chatim_iam_admin_user_limited_total 0",
		"chatim_iam_admin_inflight_rejected_total 0",
		"chatim_iam_admin_slot_timeouts_total 3",
	} {
		if !strings.Contains(body, series) {
			t.Fatalf("metrics lack %s", series)
		}
	}
}
