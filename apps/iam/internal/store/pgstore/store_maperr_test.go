package pgstore

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func unchanged(got, want error) bool {
	return errors.Is(got, want) && got.Error() == want.Error()
}

func TestMapErrKeepsDomainErrors(t *testing.T) {
	s := &Store{uri: "postgres://iam:secret-pw@db/iam"}
	pgErr := func(code, constraint string) error {
		return fmt.Errorf("exec: %w", &pgconn.PgError{Code: code, ConstraintName: constraint})
	}
	cases := []struct {
		name string
		err  error
		want error
		text string
	}{
		{"no rows", pgx.ErrNoRows, apperr.ErrNotFound, ""},
		{"group cycle", pgErr("23514", "groups_no_cycle"), apperr.ErrFailedPrecondition, "group cycle"},
		{"other check", pgErr("23514", "client_permissions_client_prefix"), apperr.ErrUnavailable, ""},
		{"serialization failure", pgErr("40001", ""), apperr.ErrUnavailable, "40001"},
		{"deadlock", pgErr("40P01", ""), apperr.ErrUnavailable, "40P01"},
		{"lock timeout", pgErr("55P03", ""), apperr.ErrUnavailable, "55P03"},
		{"statement timeout", pgErr("57014", ""), apperr.ErrUnavailable, "57014"},
		{"foreign key", pgErr("23503", "groups_tenant_id_fkey"), apperr.ErrNotFound, "groups_tenant_id_fkey"},
		{"unique", pgErr("23505", "groups_pkey"), apperr.ErrAlreadyExists, "groups_pkey"},
		{"row changed", errConflict, apperr.ErrUnavailable, ""},
		{"unknown", errors.New("dial postgres://iam:secret-pw@db/iam failed"), apperr.ErrUnavailable, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.mapErr(tc.err)
			if !errors.Is(got, tc.want) {
				t.Fatalf("mapErr = %v; want %v", got, tc.want)
			}
			if !strings.Contains(got.Error(), tc.text) || strings.Contains(got.Error(), "secret-pw") {
				t.Fatalf("mapErr text = %q; want it to name %q and hide the password", got, tc.text)
			}
			if again := s.mapErr(got); !unchanged(again, got) {
				t.Fatalf("mapErr twice = %v; want %v unchanged", again, got)
			}
		})
	}
	domain := []error{
		apperr.ErrNotFound, apperr.ErrAlreadyExists, apperr.ErrFailedPrecondition,
		apperr.ErrInvalidArgument, apperr.ErrPermissionDenied, apperr.ErrUnauthenticated, apperr.ErrUnavailable,
		fmt.Errorf("%w: missing iam:roles.write", apperr.ErrPermissionDenied),
		errGroupCycle, errGroupListedTwice,
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
