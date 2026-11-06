package objectstore

import (
	"errors"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestClassifyMapsStorageCodes(t *testing.T) {
	cases := map[string]error{
		"NoSuchUpload":          ErrNoSuchUpload,
		"InvalidPart":           ErrInvalidPart,
		"EntityTooSmall":        ErrInvalidPart,
		"NoSuchKey":             ErrNotFound,
		"NoSuchBucket":          ErrBucketMissing,
		"AccessDenied":          ErrAccess,
		"SignatureDoesNotMatch": ErrAccess,
		"InternalError":         ErrUnavailable,
		"":                      ErrUnavailable,
	}
	for code, want := range cases {
		err := classify(minio.ErrorResponse{Code: code, StatusCode: 400})
		if !errors.Is(err, want) {
			t.Errorf("%q -> %v, want %v", code, err, want)
		}
	}
	if classify(nil) != nil {
		t.Fatal("nil error classified")
	}
}

func TestStatsCountByOperationAndResult(t *testing.T) {
	var s Stats
	s.record(opComplete, nil)
	s.record(opComplete, classify(minio.ErrorResponse{Code: "NoSuchUpload"}))
	s.record(opComplete, classify(minio.ErrorResponse{Code: "InternalError"}))
	s.record(opHead, classify(minio.ErrorResponse{Code: "NoSuchKey"}))
	got := map[string]uint64{}
	for _, c := range s.Counts() {
		got[c.Operation+"/"+c.Result] = c.Read()
	}
	want := map[string]uint64{"complete/ok": 1, "complete/client": 1, "complete/error": 1, "head/client": 1, "start/ok": 0}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	if len(got) != 18 {
		t.Fatalf("%d counters", len(got))
	}
}
