package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wstest"
)

const (
	revokeWait      = 5 * time.Second
	partitionWait   = 10 * time.Second
	keySwitchWait   = 90 * time.Second
	keySwitchPoll   = 2 * time.Second
	revokeStateFile = "iam-revoke.json"
)

type revokeFlags struct {
	iamFlags
	ws2, gateway, metrics2, kid *string
}

type revokeState struct {
	Password      string `json:"password"`
	CallerAccess  string `json:"caller_access"`
	SecondAccess  string `json:"second_access"`
	SecondSession string `json:"second_session"`
}

func parseRevokeFlags(name string, args []string) (revokeFlags, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := revokeFlags{
		iamFlags: iamFlags{
			iam:     fs.String("iam", "http://chatim-iam-1:8080", "iam public url, equal to IAM_PUBLIC_URL"),
			ws:      fs.String("ws", "ws://chatim-websocket-1:8080/ws", "websocket url of the first node"),
			metrics: fs.String("metrics", "http://chatim-iam-1:9090/metrics", "iam metrics url"),
			tenant:  fs.String("tenant", "", "tenant created for this run"),
			dir:     fs.String("state", "/state", "directory holding the scenario state"),
			timeout: fs.Duration("timeout", 10*time.Second, "per-call timeout"),
		},
		ws2:      fs.String("ws2", "ws://chatim-websocket-2:8080/ws", "websocket url of the second node"),
		gateway:  fs.String("gateway", defaultGatewayURL, "gateway url"),
		metrics2: fs.String("metrics2", "http://chatim-websocket-2:9090/metrics", "metrics url of the second websocket node"),
		kid:      fs.String("kid", "", "kid the new access token must carry"),
	}
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if *f.tenant == "" {
		return f, errors.New("-tenant is required")
	}
	return f, nil
}

func e2eIAMRevoke(ctx context.Context, args []string) error {
	f, err := parseRevokeFlags("e2e iam-revoke", args)
	if err != nil {
		return err
	}
	temp, err := readSecret(*f.dir, "iam-c.pw")
	if err != nil {
		return err
	}
	c := newIAMClient(*f.iam, *f.tenant, *f.timeout)
	defer saveUsed(*f.dir, c)
	password := "e2e-" + randomHex(12)
	if err := c.changePassword(ctx, "minh", temp, password); err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	sessions := make([]iamTokens, 3)
	for i := range sessions {
		if sessions[i], err = loginOnce(ctx, c, password); err != nil {
			return err
		}
	}
	phone, second, caller := sessions[0], sessions[1], sessions[2]
	if code, err := gatewayStatus(ctx, *f.gateway, phone.Access, *f.timeout); err != nil || code != http.StatusOK {
		return fmt.Errorf("gateway before the revoke = %d, %w; want 200", code, err)
	}
	ws, err := readyConn(ctx, *f.ws, phone.Access, *f.timeout)
	if err != nil {
		return err
	}
	defer func() { _ = ws.Close() }()
	start := time.Now()
	if err := c.revokeSession(ctx, caller.Access, phone.SessionID); err != nil {
		return fmt.Errorf("revoke the phone session: %w", err)
	}
	if err := awaitRevoked(ctx, ws, revokeWait); err != nil {
		return fmt.Errorf("websocket-1: %w", err)
	}
	if err := awaitGatewayRefusal(ctx, *f.gateway, phone.Access, *f.timeout, revokeWait); err != nil {
		return err
	}
	fmt.Printf("iam: a revoked session reached websocket-1 (auth_required REVOKED) and the gateway (401) in %v\n", time.Since(start).Round(100*time.Millisecond))
	return writeJSON(filepath.Join(*f.dir, revokeStateFile), revokeState{Password: password, CallerAccess: caller.Access, SecondAccess: second.Access, SecondSession: second.SessionID})
}

func e2eIAMRevokePartitioned(ctx context.Context, args []string) error {
	f, err := parseRevokeFlags("e2e iam-revoke-partitioned", args)
	if err != nil {
		return err
	}
	var st revokeState
	if err := readJSON(filepath.Join(*f.dir, revokeStateFile), &st); err != nil {
		return err
	}
	c := newIAMClient(*f.iam, *f.tenant, *f.timeout)
	c.remember(st.CallerAccess, st.SecondAccess)
	defer saveUsed(*f.dir, c)
	series := `chatim_websocket_iam_shared_reads_total{result="merged"}`
	before, err := metricValue(ctx, *f.metrics2, series)
	if err != nil {
		return err
	}
	ws, err := readyConn(ctx, *f.ws2, st.SecondAccess, *f.timeout)
	if err != nil {
		return err
	}
	defer func() { _ = ws.Close() }()
	if err := c.revokeSession(ctx, st.CallerAccess, st.SecondSession); err != nil {
		return fmt.Errorf("revoke the second session: %w", err)
	}
	if err := awaitRevoked(ctx, ws, partitionWait); err != nil {
		return fmt.Errorf("websocket-2 cut from iam: %w", err)
	}
	after, err := metricValue(ctx, *f.metrics2, series)
	if err != nil {
		return err
	}
	if after <= before {
		return fmt.Errorf("%s stayed at %v; the revoke did not come from the shared copy", series, after)
	}
	fmt.Println("iam: websocket-2, cut from the iam internal port, applied the revoke from the shared redis copy")
	return nil
}

func e2eIAMKeyRotated(ctx context.Context, args []string) error {
	f, err := parseRevokeFlags("e2e iam-key-rotated", args)
	if err != nil {
		return err
	}
	var st revokeState
	if err := readJSON(filepath.Join(*f.dir, revokeStateFile), &st); err != nil {
		return err
	}
	c := newIAMClient(*f.iam, *f.tenant, *f.timeout)
	defer saveUsed(*f.dir, c)
	fresh, switched, err := loginWithKey(ctx, c, st.Password, *f.kid)
	if err != nil {
		return err
	}
	for _, url := range []string{*f.ws, *f.ws2} {
		ws, err := readyConn(ctx, url, fresh.Access, *f.timeout)
		if err != nil {
			return fmt.Errorf("token of the rotated key: %w", err)
		}
		_ = ws.Close()
	}
	if code, err := gatewayStatus(ctx, *f.gateway, fresh.Access, *f.timeout); err != nil || code != http.StatusOK {
		return fmt.Errorf("gateway with the rotated key = %d, %w; want 200", code, err)
	}
	fmt.Printf("iam: key-rotate alone switched signing after %v; the new token passed both websocket nodes and the gateway at once, without a restart\n", switched)
	return writeJSON(filepath.Join(*f.dir, revokeStateFile), revokeState{Password: st.Password, CallerAccess: fresh.Access})
}

func e2eIAMTenantWait(ctx context.Context, args []string) error {
	f, err := parseRevokeFlags("e2e iam-tenant-wait", args)
	if err != nil {
		return err
	}
	var st revokeState
	if err := readJSON(filepath.Join(*f.dir, revokeStateFile), &st); err != nil {
		return err
	}
	ws, err := readyConn(ctx, *f.ws, st.CallerAccess, *f.timeout)
	if err != nil {
		return err
	}
	defer func() { _ = ws.Close() }()
	if err := os.WriteFile(filepath.Join(*f.dir, "iam-tenant-ready"), nil, 0o600); err != nil {
		return err
	}
	select {
	case <-ws.Done():
	case <-time.After(3 * partitionWait):
		return errors.New("the connection stayed open after the tenant was disabled")
	}
	var closed *wstest.CloseError
	if !errors.As(ws.Err(), &closed) || closed.Code != 4003 {
		return fmt.Errorf("connection ended with %w, want close 4003", ws.Err())
	}
	if err := awaitGatewayRefusal(ctx, *f.gateway, st.CallerAccess, *f.timeout, revokeWait); err != nil {
		return err
	}
	fmt.Println("iam: tenant-disable closed the live connection with 4003 and the gateway refused its token")
	return nil
}

func loginOnce(ctx context.Context, c *iamClient, password string) (iamTokens, error) {
	p, err := newProver()
	if err != nil {
		return iamTokens{}, err
	}
	t, err := c.login(ctx, p, "minh", password)
	if err != nil {
		return iamTokens{}, fmt.Errorf("login: %w", err)
	}
	return t, nil
}

func loginWithKey(ctx context.Context, c *iamClient, password, kid string) (iamTokens, time.Duration, error) {
	started := time.Now()
	for {
		t, err := loginOnce(ctx, c, password)
		if err != nil {
			return iamTokens{}, 0, err
		}
		got := jwtverify.KeyIDOf(t.Access)
		if got == kid {
			return t, time.Since(started).Round(100 * time.Millisecond), nil
		}
		if time.Since(started) > keySwitchWait {
			return iamTokens{}, 0, fmt.Errorf("new token kid %q after %v, want the rotated key %q", got, keySwitchWait, kid)
		}
		select {
		case <-ctx.Done():
			return iamTokens{}, 0, ctx.Err()
		case <-time.After(keySwitchPoll):
		}
	}
}

func readyConn(base context.Context, url, token string, timeout time.Duration) (*wstest.Conn, error) {
	ctx, cancel := context.WithTimeout(base, timeout)
	defer cancel()
	ws, err := wstest.Dial(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	if err := ws.Auth(token); err != nil {
		_ = ws.Close()
		return nil, err
	}
	for {
		select {
		case frame := <-ws.Frames():
			switch {
			case frame.GetReady() != nil:
				return ws, nil
			case frame.GetAuthRequired() != nil:
				_ = ws.Close()
				return nil, fmt.Errorf("%s refused the token: %v", url, frame.GetAuthRequired().GetReason())
			case frame.GetRetryLater() != nil:
				_ = ws.Close()
				return nil, fmt.Errorf("%s answered retry_later: the node did not know the key yet", url)
			}
		case <-ctx.Done():
			_ = ws.Close()
			return nil, fmt.Errorf("%s: ready not received: %w", url, ctx.Err())
		}
	}
}

func awaitRevoked(base context.Context, ws *wstest.Conn, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(base, limit)
	defer cancel()
	for {
		select {
		case frame := <-ws.Frames():
			if ar := frame.GetAuthRequired(); ar != nil {
				if ar.GetReason() != chatimv1.AuthRequiredReason_AUTH_REQUIRED_REASON_REVOKED {
					return fmt.Errorf("auth_required %v, want REVOKED", ar.GetReason())
				}
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("auth_required REVOKED not received within %v", limit)
		}
	}
}

func gatewayStatus(ctx context.Context, url, token string, timeout time.Duration) (int, error) {
	g := newGateway(ctx, url, token, timeout)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := g.core.ListMyRooms(callCtx, connect.NewRequest(&chatimv1.ListMyRoomsRequest{Folder: chatimv1.RoomFolder_ROOM_FOLDER_MAIN, Limit: 1}))
	switch connect.CodeOf(err) {
	case connect.CodeUnknown:
		if err == nil {
			return http.StatusOK, nil
		}
		return 0, err
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized, nil
	case connect.CodeUnavailable:
		return http.StatusServiceUnavailable, nil
	default:
		return 0, err
	}
}

func awaitGatewayRefusal(ctx context.Context, url, token string, timeout, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		code, err := gatewayStatus(ctx, url, token, timeout)
		if err != nil {
			return err
		}
		if code == http.StatusUnauthorized {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gateway still answers %d after %v, want 401", code, limit)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
