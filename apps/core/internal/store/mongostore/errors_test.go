package mongostore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	errPreRejected         = errors.New("rejected before sending")
	errWriteConcernTimeout = &mongo.WriteConcernError{Name: "WriteConcernTimeout", Code: 64, Message: "waiting for replication timed out"}
)

func dupAt(i int) mongo.BulkWriteError {
	return mongo.BulkWriteError{WriteError: mongo.WriteError{Index: i, Code: 11000, Message: "E11000 duplicate key error"}}
}

func failedAt(i int) mongo.BulkWriteError {
	return mongo.BulkWriteError{WriteError: mongo.WriteError{Index: i, Code: 121, Message: "Document failed validation"}}
}

func bulk(wce *mongo.WriteConcernError, errs ...mongo.BulkWriteError) mongo.BulkWriteException {
	return mongo.BulkWriteException{WriteConcernError: wce, WriteErrors: errs}
}

func TestClassifyInsert(t *testing.T) {
	const I, D, U, R = store.Inserted, store.Duplicate, store.Unknown, store.Rejected
	tests := []struct {
		name   string
		sent   []int
		err    error
		want   []store.Outcome
		wantIs error
	}{
		{"no error inserts every sent doc", []int{0, 1, 2, 3, 4}, nil, []store.Outcome{I, I, I, I, I}, nil},
		{"write errors map by sent index", []int{0, 2, 4}, bulk(nil, dupAt(1), failedAt(2)), []store.Outcome{I, R, D, R, R}, apperr.ErrInvalidArgument},
		{"server write error is invalid argument", []int{0, 1}, bulk(nil, failedAt(1)), []store.Outcome{I, R}, apperr.ErrInvalidArgument},
		{"wrapped bulk exception still maps", []int{1, 3}, fmt.Errorf("op: %w", bulk(nil, dupAt(0))), []store.Outcome{R, D, R, I, R}, nil},
		{"write concern error makes clean docs unknown", []int{0, 1, 2}, bulk(errWriteConcernTimeout, dupAt(0), failedAt(2)), []store.Outcome{D, U, R, R, R}, nil},
		{"write concern error alone", []int{0, 1, 2, 3, 4}, bulk(errWriteConcernTimeout), []store.Outcome{U, U, U, U, U}, nil},
		{"empty bulk exception is unknown", []int{0, 1}, bulk(nil), []store.Outcome{U, U, R, R, R}, nil},
		{"index outside the batch is unknown", []int{0, 1, 2}, bulk(nil, dupAt(3)), []store.Outcome{U, U, U, R, R}, nil},
		{"negative index is unknown", []int{0, 1}, bulk(nil, dupAt(-1)), []store.Outcome{U, U, R, R, R}, nil},
		{"deadline is unknown", []int{0, 1, 2, 3, 4}, fmt.Errorf("insert: %w", context.DeadlineExceeded), []store.Outcome{U, U, U, U, U}, context.DeadlineExceeded},
		{"network error is unknown", []int{2, 3}, mongo.CommandError{Labels: []string{"NetworkError"}, Message: "connection reset"}, []store.Outcome{R, R, U, U, R}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := make([]store.Result, len(tt.want))
			for i := range out {
				if !slices.Contains(tt.sent, i) {
					out[i] = store.Result{Outcome: store.Rejected, Err: errPreRejected}
				}
			}
			classifyInsert(out, tt.sent, tt.err)
			for i, r := range out {
				if r.Outcome != tt.want[i] {
					t.Fatalf("result[%d] = %v, want %v; all %+v", i, r.Outcome, tt.want[i], out)
				}
				assertResultError(t, i, r, slices.Contains(tt.sent, i), tt.wantIs)
			}
		})
	}
}

func assertResultError(t *testing.T, i int, r store.Result, sent bool, wantIs error) {
	t.Helper()
	switch {
	case !sent && !errors.Is(r.Err, errPreRejected):
		t.Fatalf("result[%d] was not sent but changed to %+v", i, r)
	case !sent:
	case r.Outcome == store.Inserted || r.Outcome == store.Duplicate:
		if r.Err != nil {
			t.Fatalf("result[%d] %v carries error %v", i, r.Outcome, r.Err)
		}
	case r.Err == nil:
		t.Fatalf("result[%d] %v has no error", i, r.Outcome)
	case wantIs != nil && !errors.Is(r.Err, wantIs):
		t.Fatalf("result[%d] error %v does not wrap %v", i, r.Err, wantIs)
	}
}

func TestOnlyDuplicateKeys(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"all duplicates", bulk(nil, dupAt(0), dupAt(3)), true},
		{"wrapped duplicates", fmt.Errorf("insert: %w", bulk(nil, dupAt(1))), true},
		{"duplicate and other failure", bulk(nil, dupAt(0), failedAt(1)), false},
		{"duplicates with write concern error", bulk(errWriteConcernTimeout, dupAt(0)), false},
		{"empty bulk exception", bulk(nil), false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := onlyDuplicateKeys(tt.err); got != tt.want {
				t.Fatalf("onlyDuplicateKeys = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNamespaceExists(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"namespace exists", mongo.CommandError{Code: 48, Name: "NamespaceExists"}, true},
		{"wrapped namespace exists", fmt.Errorf("create: %w", mongo.CommandError{Code: 48}), true},
		{"other command error", mongo.CommandError{Code: 13, Name: "Unauthorized"}, false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := namespaceExists(tt.err); got != tt.want {
				t.Fatalf("namespaceExists = %v, want %v", got, tt.want)
			}
		})
	}
}
