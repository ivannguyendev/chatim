package jwtverify

import "errors"

var (
	ErrInvalidToken  = errors.New("invalid token")
	ErrInvalidKeySet = errors.New("invalid key set")
	ErrInvalidConfig = errors.New("invalid jwt verifier config")
)

type Reason string

const (
	ReasonMalformed    Reason = "malformed"
	ReasonAlgorithm    Reason = "algorithm"
	ReasonSignature    Reason = "bad signature"
	ReasonKeyID        Reason = "unknown kid"
	ReasonType         Reason = "bad typ"
	ReasonIssuer       Reason = "bad issuer"
	ReasonAudience     Reason = "bad audience"
	ReasonExpired      Reason = "expired"
	ReasonNotYetValid  Reason = "not yet valid"
	ReasonMissingClaim Reason = "missing claim"
	ReasonSubject      Reason = "bad subject"
	ReasonTenant       Reason = "bad tenant"
	ReasonClaims       Reason = "bad claims"
)

type tokenError struct {
	reason Reason
}

func (e tokenError) Error() string { return ErrInvalidToken.Error() + ": " + string(e.reason) }

func (e tokenError) Unwrap() error { return ErrInvalidToken }

func invalid(reason Reason) error { return tokenError{reason: reason} }

func ReasonOf(err error) Reason {
	if te, ok := errors.AsType[tokenError](err); ok {
		return te.reason
	}
	return ""
}
