package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

type row struct {
	check, result, detail string
}

type spike struct {
	cfg      config
	store    objectStore
	signer   objectStore
	badCreds objectStore
	down     objectStore
	rows     []row
}

func (s *spike) add(check string, ok bool, format string, args ...any) {
	result := "FAIL"
	if ok {
		result = "PASS"
	}
	s.rows = append(s.rows, row{check: check, result: result, detail: fmt.Sprintf(format, args...)})
}

func (s *spike) key(name string) string {
	return fmt.Sprintf("spike/%s/%s/%d", s.cfg.sdk, name, time.Now().UnixNano())
}

func (s *spike) lifecycleOfOneFile(ctx context.Context) {
	const partSize = 5 << 20
	sizes := splitSizes(11<<20, partSize)
	chunk := randomBytes(partSize)
	key := s.key("main")
	id, err := s.store.start(ctx, key, "video/mp4")
	s.add("a start multipart", err == nil, "err=%s", s.store.code(err))
	if err != nil {
		return
	}
	sp, err := s.store.signPart(ctx, key, id, 1, sizes[0], 15*time.Minute)
	if err != nil {
		s.add("b signed content-length", false, "sign err=%v", err)
		return
	}
	wrong, werr := put(ctx, sp, randomBytes(sizes[0]+1), "")
	s.add("b signed content-length", werr == nil && wrong.status >= 400, "PUT size+1 status=%d err=%v", wrong.status, werr)
	parts, err := uploadAll(ctx, s.store, key, id, sizes, chunk, 3)
	if err != nil {
		s.add("c complete with client etags", false, "upload err=%v", err)
		return
	}
	bad := slices.Clone(parts)
	bad[1].etag = `"00000000000000000000000000000000"`
	errBad := s.store.complete(ctx, key, id, bad)
	errOK := s.store.complete(ctx, key, id, parts)
	size, herr := s.store.head(ctx, s.cfg.bucket, key)
	s.add("c complete with client etags", errBad != nil && errOK == nil && herr == nil && size == 11<<20,
		"wrong etag=%s ok=%s head size=%d", s.store.code(errBad), s.store.code(errOK), size)
	s.subsetComplete(ctx, sizes, chunk)
	again, aerr := put(ctx, sp, chunk[:sizes[0]], "")
	s.add("e PUT after complete", aerr == nil && again.status >= 400, "status=%d", again.status)
	errTwice := s.store.complete(ctx, key, id, parts)
	size2, herr2 := s.store.head(ctx, s.cfg.bucket, key)
	twiceCode := s.store.code(errTwice)
	s.add("f complete twice then head", (twiceCode == "NoSuchUpload" || twiceCode == "") && herr2 == nil && size2 == 11<<20,
		"second complete=%q head size=%d", twiceCode, size2)
}

func (s *spike) subsetComplete(ctx context.Context, sizes []int64, chunk []byte) {
	key := s.key("subset")
	id, err := s.store.start(ctx, key, "video/mp4")
	if err == nil {
		var parts []part
		parts, err = uploadAll(ctx, s.store, key, id, sizes, chunk, 3)
		if err == nil {
			err = s.store.complete(ctx, key, id, []part{parts[0], parts[2]})
		}
	}
	size, _ := s.store.head(ctx, s.cfg.bucket, key)
	s.rows = append(s.rows, row{check: "c2 complete with parts 1 and 3 only", result: "INFO",
		detail: fmt.Sprintf("complete=%q head size=%d of %d: server must check parts 1..n and total size itself", s.store.code(err), size, 11<<20)})
}

func (s *spike) manyParts(ctx context.Context) {
	tiny := []int64{}
	for range s.cfg.manyParts {
		tiny = append(tiny, 1024)
	}
	if ok := s.timedComplete(ctx, "d complete many tiny parts", tiny); ok {
		return
	}
	var real []int64
	for range s.cfg.fallbackParts {
		real = append(real, 5<<20)
	}
	s.timedComplete(ctx, "d complete many 5MiB parts", real)
}

func (s *spike) timedComplete(ctx context.Context, check string, sizes []int64) bool {
	key := s.key("many")
	id, err := s.store.start(ctx, key, "application/octet-stream")
	if err != nil {
		s.add(check, false, "start=%s", s.store.code(err))
		return false
	}
	parts, err := uploadAll(ctx, s.store, key, id, sizes, randomBytes(slices.Max(sizes)), 16)
	if err != nil {
		s.add(check, false, "upload err=%v", err)
		return false
	}
	began := time.Now()
	err = s.store.complete(ctx, key, id, parts)
	took := time.Since(began)
	s.add(check, err == nil && took < 5*time.Second, "parts=%d complete=%s took=%s", len(sizes), s.store.code(err), took.Round(time.Millisecond))
	if err != nil {
		_ = s.store.abort(ctx, key, id)
	}
	return err == nil
}

func (s *spike) copyLarge(ctx context.Context) {
	if s.cfg.copyBytes == 0 {
		return
	}
	const partSize = 16 << 20
	key := s.key("copy")
	id, err := s.store.start(ctx, key, "video/mp4")
	if err != nil {
		s.add("g copy processing to completed", false, "start=%s", s.store.code(err))
		return
	}
	parts, err := uploadAll(ctx, s.store, key, id, splitSizes(s.cfg.copyBytes, partSize), randomBytes(partSize), 8)
	if err == nil {
		err = s.store.complete(ctx, key, id, parts)
	}
	if err != nil {
		s.add("g copy processing to completed", false, "upload=%v", err)
		return
	}
	began := time.Now()
	err = s.store.copyObject(ctx, s.cfg.bucket, s.cfg.completed, key)
	took := time.Since(began)
	size, herr := s.store.head(ctx, s.cfg.completed, key)
	s.add("g copy processing to completed", err == nil && herr == nil && size == s.cfg.copyBytes,
		"bytes=%d copy=%s took=%s", size, s.store.code(err), took.Round(time.Millisecond))
}

func (s *spike) publicEndpoint(ctx context.Context) {
	if s.signer == nil {
		return
	}
	key := s.key("public")
	id, err := s.store.start(ctx, key, "image/png")
	if err != nil {
		s.add("h public endpoint signing", false, "start=%s", s.store.code(err))
		return
	}
	defer func() { _ = s.store.abort(ctx, key, id) }()
	sp, err := s.signer.signPart(ctx, key, id, 1, 1024, time.Minute)
	if err != nil {
		s.add("h public endpoint signing", false, "sign=%v", err)
		return
	}
	body := randomBytes(1024)
	viaPublic, perr := put(ctx, sp, body, "")
	internal := sp
	internal.url = withHost(sp.url, strings.TrimPrefix(strings.TrimPrefix(s.cfg.endpoint, "http://"), "https://"))
	viaInternal, ierr := put(ctx, internal, body, "")
	s.add("h public endpoint signing", perr == nil && viaPublic.status == http.StatusOK && ierr == nil && viaInternal.status >= 400,
		"public host status=%d, same URL on internal host status=%d", viaPublic.status, viaInternal.status)
}

func (s *spike) corsFromBrowser(ctx context.Context) {
	origin := s.cfg.origin
	err := s.store.putCORS(ctx, s.cfg.bucket, origin)
	if err != nil {
		s.add("i CORS put and expose ETag", false, "PutBucketCors=%s", s.store.code(err))
		return
	}
	key := s.key("cors")
	id, err := s.store.start(ctx, key, "image/png")
	if err != nil {
		s.add("i CORS put and expose ETag", false, "start=%s", s.store.code(err))
		return
	}
	defer func() { _ = s.store.abort(ctx, key, id) }()
	sp, _ := s.store.signPart(ctx, key, id, 1, 1024, time.Minute)
	pre, status, perr := preflight(ctx, sp.url, origin)
	r, rerr := put(ctx, sp, randomBytes(1024), origin)
	exposed := strings.Contains(strings.ToLower(r.header.Get("Access-Control-Expose-Headers")), "etag")
	s.add("i CORS put and expose ETag", perr == nil && rerr == nil && pre.Get("Access-Control-Allow-Origin") != "" && r.status == http.StatusOK && exposed,
		"preflight status=%d allow-origin=%q put status=%d expose=%q", status, pre.Get("Access-Control-Allow-Origin"), r.status, r.header.Get("Access-Control-Expose-Headers"))
}

func (s *spike) bucketChecks(ctx context.Context) {
	ok := s.store.headBucket(ctx, s.cfg.bucket)
	missing := s.store.headBucket(ctx, "spike-missing-bucket")
	creds := s.badCreds.headBucket(ctx, s.cfg.bucket)
	down := s.down.headBucket(ctx, s.cfg.bucket)
	s.add("j headBucket classification", ok == nil && missing != nil && creds != nil && down != nil,
		"ok=%q missing=%q bad creds=%q down=%q", s.store.code(ok), s.store.code(missing), s.badCreds.code(creds), s.down.code(down))
	err := s.store.putLifecycle(ctx, s.cfg.bucket)
	s.add("k lifecycle rules", err == nil, "PutBucketLifecycle=%s", s.store.code(err))
}
