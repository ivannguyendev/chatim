package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/lxzan/gws"

	"github.com/ivannguyendev/chatim/internal/wstest"
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
		if err := r.refused("a token signed by an unknown key with kid "+kid, token); err != nil {
			return err
		}
	}
	return nil
}

func (r *wsRun) refused(what, token string) error {
	c, err := r.dial(token)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	select {
	case <-c.Done():
	case <-time.After(r.callTimeout):
		return fmt.Errorf("%s stayed connected for %v, want close 4001", what, r.callTimeout)
	case <-r.base.Done():
		return r.base.Err()
	}
	closeErr, ok := errors.AsType[*wstest.CloseError](c.Err())
	if !ok || closeErr.Code != wstest.CodeUnauthenticated {
		return fmt.Errorf("%s closed with %w, want code %d", what, c.Err(), wstest.CodeUnauthenticated)
	}
	return nil
}

type silentWatch struct {
	gws.BuiltinEventHandler
	closed chan error
}

func (w *silentWatch) OnClose(_ *gws.Conn, err error) { w.closed <- err }

func (r *wsRun) silentTimesOut() error {
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
		return fmt.Errorf("a connection without auth stayed open for %v, want close 4002", r.callTimeout)
	}
	waited := time.Since(start)
	code, ok := e2e.CloseCode(closeErr)
	if !ok {
		return fmt.Errorf("a connection without auth ended with %w after %v, want close 4002", closeErr, waited)
	}
	if err := e2e.CheckAuthTimeout(code, waited, r.authTimeout); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "a connection without auth closed with code %d after %v\n", code, waited.Round(10*time.Millisecond))
	return nil
}
