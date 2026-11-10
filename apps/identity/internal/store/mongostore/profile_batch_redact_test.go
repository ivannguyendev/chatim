package mongostore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const leakedSubjectMessage = `E11000 duplicate key error collection: chatim_identity.user_links index: _id_ dup key: { _id: "acme│staff│0901000000" }`

func TestJITBatchKeepsTenantSubjectsOutOfErrors(t *testing.T) {
	cases := map[string]func(ctx context.Context, ids []string) error{
		"write concern with a duplicate": func(context.Context, []string) error {
			return mongo.BulkWriteException{
				WriteConcernError: &mongo.WriteConcernError{Code: 64, Message: "waiting for replication timed out for acme│staff│0901000000"},
				WriteErrors:       []mongo.BulkWriteError{{WriteError: mongo.WriteError{Index: 0, Code: duplicateKeyCode, Message: leakedSubjectMessage}}},
			}
		},
		"write errors out of range": func(context.Context, []string) error {
			return mongo.BulkWriteException{
				WriteErrors: []mongo.BulkWriteError{{WriteError: mongo.WriteError{Index: 9, Code: documentValidationCode, Message: leakedSubjectMessage}}},
			}
		},
		"one document rejected": func(context.Context, []string) error {
			return mongo.BulkWriteException{
				WriteErrors: []mongo.BulkWriteError{
					{WriteError: mongo.WriteError{Index: 0, Code: documentValidationCode, Message: leakedSubjectMessage}},
					{WriteError: mongo.WriteError{Index: 1, Code: documentValidationCode, Message: leakedSubjectMessage}},
				},
			}
		},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
			synctest.Test(t, func(t *testing.T) {
				b := newTestBatcher(t, testJITConfig(), &fakeInserts{reply: reply})
				out := make(chan writeOutcome, 2)
				startWrite(t.Context(), b, "t│a", out)
				startWrite(t.Context(), b, "t│b", out)
				for id, o := range collectOutcomes(t, out, 2) {
					if o.written || !errors.Is(o.err, apperr.ErrUnavailable) {
						t.Fatalf("write %s = %v, %v; want unavailable", id, o.written, o.err)
					}
					if text := o.err.Error(); strings.Contains(text, "0901000000") || strings.Contains(text, "│") {
						t.Fatalf("write %s error %q; want no tenant subject", id, text)
					}
				}
			})
		})
	}
}
