package main

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/pkg/wstest"
)

type iamFlags struct {
	iam, ws, metrics, tenant, dir *string
	timeout                       *time.Duration
}

type iamState struct {
	Refresh string `json:"refresh"`
	Key     []byte `json:"key"`
}

func parseIAMFlags(name string, args []string) (iamFlags, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := iamFlags{
		iam:     fs.String("iam", "http://chatim-iam-1:8080", "iam public url, equal to IAM_PUBLIC_URL"),
		ws:      fs.String("ws", "ws://chatim-websocket-1:8080/ws", "websocket url"),
		metrics: fs.String("metrics", "http://chatim-iam-1:9090/metrics", "iam metrics url"),
		tenant:  fs.String("tenant", "", "tenant created for this run"),
		dir:     fs.String("state", "/state", "directory holding the scenario state"),
		timeout: fs.Duration("timeout", 10*time.Second, "per-call timeout"),
	}
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if *f.tenant == "" {
		return f, errors.New("-tenant is required")
	}
	return f, nil
}

func readSecret(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	return strings.TrimSpace(string(b)), err
}

func e2eIAMLogin(ctx context.Context, args []string) error {
	f, err := parseIAMFlags("e2e iam-login", args)
	if err != nil {
		return err
	}
	tempA, err := readSecret(*f.dir, "iam-a.pw")
	if err != nil {
		return err
	}
	tempB, err := readSecret(*f.dir, "iam-b.pw")
	if err != nil {
		return err
	}
	c := newIAMClient(*f.iam, *f.tenant, *f.timeout)
	defer saveUsed(*f.dir, c)
	lan, err := newProver()
	if err != nil {
		return err
	}
	if _, err := c.login(ctx, lan, "lan", tempA); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		return fmt.Errorf("login with the temporary password = %w, want failed_precondition", err)
	}
	newPassword := "e2e-" + randomHex(12)
	if err := c.changePassword(ctx, "lan", tempA, newPassword); err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	first, err := c.login(ctx, lan, "lan", newPassword)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	fmt.Println("iam: temporary password forced a change, then login returned a session")
	if err := websocketReady(ctx, *f.ws, first.Access, *f.timeout); err != nil {
		return err
	}
	fmt.Println("iam: the websocket accepted the iam access token")
	latest, err := c.mustRefresh(ctx, lan, first.Refresh, "refresh")
	if err != nil {
		return err
	}
	for i := range 2 {
		if latest, err = c.mustRefresh(ctx, lan, first.Refresh, fmt.Sprintf("retry %d of the replaced token inside the grace window", i+1)); err != nil {
			return err
		}
	}
	thief, err := newProver()
	if err != nil {
		return err
	}
	if err := c.refused(ctx, thief, latest.Refresh, "refresh signed by another device key"); err != nil {
		return err
	}
	if latest, err = c.mustRefresh(ctx, lan, latest.Refresh, "refresh after the other key was refused"); err != nil {
		return err
	}
	fmt.Println("iam: refresh rotated, retries inside the grace window passed, another device key was refused without ending the session")
	if err := secondSession(ctx, c, newPassword, latest); err != nil {
		return err
	}
	sid, _, _ := strings.Cut(latest.Refresh, ".")
	if err := c.refused(ctx, lan, sid+"."+strings.Repeat("A", 43), "a real session id with a garbage secret"); err != nil {
		return err
	}
	if latest, err = c.mustRefresh(ctx, lan, latest.Refresh, "refresh after the garbage secret"); err != nil {
		return err
	}
	fmt.Println("iam: a garbage secret with a real session id was refused and the session lived on")
	if err := throttled(ctx, c, *f.metrics, tempB); err != nil {
		return err
	}
	key, err := x509.MarshalECPrivateKey(lan.key)
	if err != nil {
		return err
	}
	return writeIAMState(*f.dir, iamState{Refresh: latest.Refresh, Key: key})
}

func secondSession(ctx context.Context, c *iamClient, password string, current iamTokens) error {
	laptop, err := newProver()
	if err != nil {
		return err
	}
	second, err := c.login(ctx, laptop, "lan", password)
	if err != nil {
		return fmt.Errorf("second login: %w", err)
	}
	list, err := c.sessions(ctx, current.Access)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	marked := 0
	for _, s := range list {
		if s.GetCurrent() {
			marked++
		}
	}
	if len(list) != 2 || marked != 1 {
		return fmt.Errorf("sessions = %d with %d current, want 2 and 1", len(list), marked)
	}
	if err := c.revokeSession(ctx, current.Access, second.SessionID); err != nil {
		return fmt.Errorf("revoke the second session: %w", err)
	}
	if err := c.refused(ctx, laptop, second.Refresh, "refresh of the revoked session"); err != nil {
		return err
	}
	fmt.Println("iam: two sessions listed with one current; the revoked one could not refresh")
	return nil
}

func throttled(ctx context.Context, c *iamClient, metricsURL, tempB string) error {
	series := `chatim_iam_login_total{result="throttled_account"}`
	before, err := metricValue(ctx, metricsURL, series)
	if err != nil {
		return err
	}
	binh, err := newProver()
	if err != nil {
		return err
	}
	for range 6 {
		_, _ = c.login(ctx, binh, "binh", "wrong-password-value")
	}
	if _, err := c.login(ctx, binh, "binh", tempB); connect.CodeOf(err) != connect.CodeUnauthenticated {
		return fmt.Errorf("right password while throttled = %w, want unauthenticated", err)
	}
	after, err := metricValue(ctx, metricsURL, series)
	if err != nil {
		return err
	}
	if after <= before {
		return fmt.Errorf("%s stayed at %v", series, after)
	}
	fmt.Println("iam: after six wrong passwords the right one was refused with the same error while the account waited")
	return nil
}

func e2eIAMDisabled(ctx context.Context, args []string) error {
	f, err := parseIAMFlags("e2e iam-disabled", args)
	if err != nil {
		return err
	}
	st, err := readIAMState(*f.dir)
	if err != nil {
		return err
	}
	key, err := x509.ParseECPrivateKey(st.Key)
	if err != nil {
		return err
	}
	c := newIAMClient(*f.iam, *f.tenant, *f.timeout)
	if err := c.refused(ctx, &prover{key: key}, st.Refresh, "refresh after account-disable"); err != nil {
		return err
	}
	fmt.Println("iam: account-disable ended the live session")
	return nil
}

func websocketReady(base context.Context, url, token string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(base, timeout)
	defer cancel()
	ws, err := wstest.Dial(ctx, url)
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}
	defer func() { _ = ws.Close() }()
	if err := ws.Auth(token); err != nil {
		return err
	}
	for {
		select {
		case frame := <-ws.Frames():
			if frame.GetReady() != nil {
				return nil
			}
			if ar := frame.GetAuthRequired(); ar != nil {
				return fmt.Errorf("websocket refused the iam token: %v", ar.GetReason())
			}
		case <-ctx.Done():
			return fmt.Errorf("websocket ready not received: %w", ctx.Err())
		}
	}
}

func writeIAMState(dir string, st iamState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "iam.json"), b, 0o600)
}

func readIAMState(dir string) (iamState, error) {
	var st iamState
	b, err := os.ReadFile(filepath.Join(dir, "iam.json"))
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

func saveUsed(dir string, c *iamClient) {
	_ = os.WriteFile(filepath.Join(dir, "iam-secrets"), []byte(strings.Join(c.used, "\n")+"\n"), 0o600)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
