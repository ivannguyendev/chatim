package objectstore

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

type s3Call struct {
	Method, Path, Source, Range, ContentType, Directive, PartNumber string
}

type fakeS3 struct {
	mu          sync.Mutex
	calls       []s3Call
	failPart    string
	headSize    int64
	headType    string
	headMissing bool
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	call := s3Call{
		Method: r.Method, Path: r.URL.Path, Source: r.Header.Get("X-Amz-Copy-Source"), Range: r.Header.Get("X-Amz-Copy-Source-Range"),
		ContentType: r.Header.Get("Content-Type"), Directive: r.Header.Get("X-Amz-Metadata-Directive"), PartNumber: q.Get("partNumber"),
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	failPart, headSize, headType, headMissing := f.failPart, f.headSize, f.headType, f.headMissing
	f.mu.Unlock()
	const stamp = "<LastModified>2026-11-08T00:00:00.000Z</LastModified>"
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		fmt.Fprint(w, "<InitiateMultipartUploadResult><Bucket>c</Bucket><Key>k</Key><UploadId>u1</UploadId></InitiateMultipartUploadResult>")
	case r.Method == http.MethodPut && call.PartNumber != "" && call.PartNumber == failPart:
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "<Error><Code>InvalidRange</Code><Message>bad range</Message></Error>")
	case r.Method == http.MethodPut && call.PartNumber != "":
		fmt.Fprintf(w, `<CopyPartResult><ETag>"p%s"</ETag>%s</CopyPartResult>`, call.PartNumber, stamp)
	case r.Method == http.MethodPut:
		fmt.Fprintf(w, `<CopyObjectResult><ETag>"e"</ETag>%s</CopyObjectResult>`, stamp)
	case r.Method == http.MethodPost && q.Has("uploadId"):
		fmt.Fprint(w, `<CompleteMultipartUploadResult><Bucket>c</Bucket><Key>k</Key><ETag>"m-3"</ETag></CompleteMultipartUploadResult>`)
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodHead && headMissing:
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodHead:
		w.Header().Set("Content-Length", strconv.FormatInt(headSize, 10))
		w.Header().Set("Content-Type", headType)
		w.Header().Set("ETag", `"e"`)
		w.Header().Set("Last-Modified", "Sun, 08 Nov 2026 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func (f *fakeS3) recorded() []s3Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]s3Call(nil), f.calls...)
}

func fakeStore(t *testing.T, f *fakeS3) *S3 {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s, err := New(Config{
		Endpoint: srv.URL, PublicEndpoint: srv.URL, Region: "us-east-1", AccessKey: "GK1", SecretKey: "secret",
		Bucket: "processing", CompletedBucket: "completed", Copy: testCopyLimits,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.CloseIdle)
	return s
}

func counted(s *S3, op string) map[string]uint64 {
	out := map[string]uint64{}
	for _, c := range s.Stats().Counts() {
		if c.Operation == op {
			out[c.Result] = c.Read()
		}
	}
	return out
}
