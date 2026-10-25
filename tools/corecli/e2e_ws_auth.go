package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/lxzan/gws"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/internal/wstest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

func (r *wsRun) rogueKeyRefused() error {
	for _, kid := range []string{"rogue", r.key.KeyID} {
		rogue, err := devtoken.Generate(kid)
		if err != nil {
			return err
		}
		token, err := r.sign(rogue, time.Now(), devTokenTTL)
		if err != nil {
			return err
		}
		if err := r.authRequired("a token signed by an unknown key with kid "+kid, token, chatimv1.AuthRequiredReason_AUTH_REQUIRED_REASON_INVALID); err != nil {
			return err
		}
	}
	return nil
}

func (r *wsRun) authRequired(what, token string, want chatimv1.AuthRequiredReason) error {
	c, err := r.dial(token)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	select {
	case f, ok := <-c.Frames():
		if !ok {
			return fmt.Errorf("%s closed with %w, want auth_required %v and the connection kept", what, c.Err(), want)
		}
		if err := e2e.CheckAuthRequired(f, want); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
	case <-time.After(r.callTimeout):
		return fmt.Errorf("%s got no frame for %v, want auth_required %v", what, r.callTimeout, want)
	case <-r.base.Done():
		return r.base.Err()
	}
	if _, err := callWS(r.base, c, r.callTimeout, historyOverWS()); status.Code(err) != codes.Unauthenticated {
		return fmt.Errorf("%s: a request after auth_required = %w, want Unauthenticated on the open connection", what, err)
	}
	return nil
}

type silentWatch struct {
	gws.BuiltinEventHandler
	closed chan error
}

func (w *silentWatch) OnClose(_ *gws.Conn, err error) { w.closed <- err }

func (r *wsRun) silentClosed() error {
	w := &silentWatch{closed: make(chan error, 1)}
	conn, resp, err := gws.NewClient(w, &gws.ClientOption{
		Addr:             r.url,
		RequestHeader:    http.Header{"Sec-Websocket-Protocol": []string{wstest.Subprotocol}},
		HandshakeTimeout: r.callTimeout,
	})
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("dial without auth: %w", err)
	}
	start := time.Now()
	go conn.ReadLoop()
	var closeErr error
	select {
	case closeErr = <-w.closed:
	case <-time.After(r.callTimeout):
		_ = conn.NetConn().Close()
		<-w.closed
		return fmt.Errorf("a silent connection stayed open for %v, want close 1000", r.callTimeout)
	}
	waited := time.Since(start)
	code, ok := e2e.CloseCode(closeErr)
	if !ok {
		return fmt.Errorf("a silent connection ended with %w after %v, want close 1000", closeErr, waited)
	}
	if err := e2e.CheckSilentClose(code, waited, r.silentClose); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "a silent connection closed with code %d after %v\n", code, waited.Round(10*time.Millisecond))
	return nil
}
