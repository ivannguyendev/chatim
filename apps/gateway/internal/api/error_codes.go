package api

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

type claimsKey struct{}

func withClaims(ctx context.Context, c jwtverify.Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

func ClaimsFrom(ctx context.Context) (jwtverify.Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(jwtverify.Claims)
	return c, ok
}

func connectCode(c codes.Code) connect.Code {
	if c <= codes.OK || c > codes.Unauthenticated {
		return connect.CodeUnknown
	}
	return connect.Code(c)
}

var plainStatus = map[connect.Code]int{
	connect.CodeUnauthenticated:   http.StatusUnauthorized,
	connect.CodeResourceExhausted: http.StatusTooManyRequests,
	connect.CodeUnavailable:       http.StatusServiceUnavailable,
	connect.CodeUnimplemented:     http.StatusNotFound,
}

type errorWriter struct {
	connect *connect.ErrorWriter
}

func newErrorWriter() errorWriter {
	return errorWriter{connect: connect.NewErrorWriter()}
}

func (w errorWriter) write(resp http.ResponseWriter, req *http.Request, code connect.Code, message string) {
	if w.connect.IsSupported(req) {
		_ = w.connect.Write(resp, req, connect.NewError(code, messageError(message)))
		return
	}
	status, ok := plainStatus[code]
	if !ok {
		status = http.StatusInternalServerError
	}
	http.Error(resp, message, status)
}

type messageError string

func (e messageError) Error() string { return string(e) }
