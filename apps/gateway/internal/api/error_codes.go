package api

import (
	"context"
	"encoding/json"
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

var statusOf = map[connect.Code]int{
	connect.CodeUnauthenticated:   http.StatusUnauthorized,
	connect.CodeResourceExhausted: http.StatusTooManyRequests,
	connect.CodeUnavailable:       http.StatusServiceUnavailable,
	connect.CodeUnimplemented:     http.StatusNotFound,
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, code connect.Code, message string) {
	status, ok := statusOf[code]
	if !ok {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code.String(), Message: message})
}

type messageError string

func (e messageError) Error() string { return string(e) }
