package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wstest"
)

var (
	chatTokenClaims = []string{"aud", "exp", "iat", "iss", "sid", "sub", "tid"}
	chatUserID      = regexp.MustCompile(`^[a-z2-7]{26}$`)
	userIDEncoding  = base32.StdEncoding.WithPadding(base32.NoPadding)
)

type usedSecrets struct {
	path string
	list []string
}

func (u *usedSecrets) remember(secrets ...string) {
	for _, s := range secrets {
		if s != "" {
			u.list = append(u.list, s)
		}
	}
}

func (u *usedSecrets) save() error {
	if len(u.list) == 0 {
		return nil
	}
	f, err := os.OpenFile(filepath.Clean(u.path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(strings.Join(u.list, "\n") + "\n")
	return errors.Join(werr, f.Close())
}

func metricValue(ctx context.Context, metricsURL, series string) (float64, error) {
	values, err := metricLines(ctx, metricsURL, func(line string) bool { return strings.HasPrefix(line, series+" ") })
	if err != nil {
		return 0, err
	}
	if len(values) == 0 {
		return 0, fmt.Errorf("series %s not found", series)
	}
	return values[0], nil
}

func metricSum(ctx context.Context, metricsURL, name string) (float64, error) {
	values, err := metricLines(ctx, metricsURL, func(line string) bool {
		return strings.HasPrefix(line, name+"{") || strings.HasPrefix(line, name+" ")
	})
	if err != nil {
		return 0, err
	}
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total, nil
}

func metricLines(ctx context.Context, metricsURL string, match func(string) bool) ([]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out []float64
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !match(line) {
			continue
		}
		value, err := strconv.ParseFloat(line[strings.LastIndexByte(line, ' ')+1:], 64)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, scanner.Err()
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
				return fmt.Errorf("websocket refused the chat token: %v", ar.GetReason())
			}
		case <-ctx.Done():
			return fmt.Errorf("websocket ready not received: %w", ctx.Err())
		}
	}
}

func gatewayStatus(ctx context.Context, url, token string, timeout time.Duration) (int, error) {
	g := newGateway(ctx, url, token, timeout)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := g.chat.ListMyRooms(callCtx, connect.NewRequest(&chatimv1.ListMyRoomsRequest{Folder: chatimv1.RoomFolder_ROOM_FOLDER_MAIN, Limit: 1}))
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

func checkChatToken(token, tenant, tenantSub string, ttl time.Duration) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("chat token has %d parts, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode chat token claims: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return "", fmt.Errorf("decode chat token claims: %w", err)
	}
	names := make([]string, 0, len(claims))
	for name := range claims {
		names = append(names, name)
	}
	slices.Sort(names)
	if !slices.Equal(names, chatTokenClaims) {
		return "", fmt.Errorf("chat token claims = %v, want %v", names, chatTokenClaims)
	}
	exp, okExp := claims["exp"].(float64)
	iat, okIat := claims["iat"].(float64)
	if !okExp || !okIat || time.Duration(exp-iat)*time.Second != ttl {
		return "", fmt.Errorf("chat token exp - iat = %v - %v, want %v", claims["exp"], claims["iat"], ttl)
	}
	sub, _ := claims["sub"].(string)
	if claims["tid"] != tenant || !chatUserID.MatchString(sub) || sub == tenantSub {
		return "", fmt.Errorf("chat token tid, sub = %v, %v; want %s and a 26-char lowercase base32 id other than the tenant sub %q", claims["tid"], claims["sub"], tenant, tenantSub)
	}
	return sub, nil
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Clean(path), b, 0o600)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func readSecret(path string) (string, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	return strings.TrimSpace(string(b)), err
}

func randomUserID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return strings.ToLower(userIDEncoding.EncodeToString(b))
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
