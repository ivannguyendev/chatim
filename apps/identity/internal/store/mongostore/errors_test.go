package mongostore

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func unchanged(got, want error) bool {
	return errors.Is(got, want) && got.Error() == want.Error()
}

func TestMapErrSortsDriverErrors(t *testing.T) {
	s := &Store{uri: "mongodb://identity:secret-pw@db:27017/chatim_identity"}
	duplicate := mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: 11000, Message: "E11000 duplicate key"}}}
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"no documents", mongo.ErrNoDocuments, apperr.ErrNotFound},
		{"write conflict", mongo.CommandError{Code: 112, Message: "WriteConflict"}, apperr.ErrUnavailable},
		{"transient transaction", mongo.CommandError{Code: 251, Labels: []string{"TransientTransactionError"}}, apperr.ErrUnavailable},
		{"unknown commit result", mongo.CommandError{Code: 50, Labels: []string{"UnknownTransactionCommitResult"}}, apperr.ErrUnavailable},
		{"duplicate key", fmt.Errorf("insert: %w", duplicate), apperr.ErrAlreadyExists},
		{"duplicate key in a transient transaction", mongo.WriteException{
			WriteErrors: mongo.WriteErrors{{Code: 11000, Message: "E11000 duplicate key"}},
			Labels:      []string{"TransientTransactionError"},
		}, apperr.ErrUnavailable},
		{"network", errors.New("dial mongodb://identity:secret-pw@db:27017/chatim_identity: connection refused"), apperr.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.mapErr(tc.err)
			if !errors.Is(got, tc.want) {
				t.Fatalf("mapErr = %v; want %v", got, tc.want)
			}
			if !errors.Is(tc.want, apperr.ErrUnavailable) && errors.Is(got, apperr.ErrUnavailable) {
				t.Fatalf("mapErr = %v; want it not unavailable", got)
			}
			if strings.Contains(got.Error(), "secret-pw") {
				t.Fatalf("mapErr text = %q; want the password hidden", got)
			}
			if again := s.mapErr(got); !unchanged(again, got) {
				t.Fatalf("mapErr twice = %v; want %v unchanged", again, got)
			}
		})
	}
}

func TestMapErrKeepsDomainErrors(t *testing.T) {
	s := &Store{uri: "mongodb://db:27017"}
	domain := []error{
		apperr.ErrNotFound, apperr.ErrAlreadyExists, apperr.ErrFailedPrecondition,
		apperr.ErrInvalidArgument, apperr.ErrPermissionDenied, apperr.ErrUnauthenticated, apperr.ErrUnavailable,
		fmt.Errorf("%w: group cycle", apperr.ErrFailedPrecondition),
	}
	for _, err := range domain {
		if got := s.mapErr(err); !unchanged(got, err) {
			t.Fatalf("mapErr(%v) = %v; want it unchanged", err, got)
		}
	}
	if s.mapErr(nil) != nil {
		t.Fatal("mapErr(nil) is not nil")
	}
}
