package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type config struct {
	server, sdk              string
	endpoint, public, region string
	access, secret           string
	bucket, completed        string
	origin                   string
	manyParts, fallbackParts int
	copyBytes                int64
}

func main() { os.Exit(realMain()) }

func realMain() int {
	var c config
	flag.StringVar(&c.server, "server", "garage", "server label for the report")
	flag.StringVar(&c.sdk, "sdk", "minio", "SDK label for the report; the spike uses minio-go")
	flag.StringVar(&c.endpoint, "endpoint", cmp.Or(os.Getenv("S3_ENDPOINT"), "http://chatim-spike-garage:3900"), "internal S3 endpoint (env S3_ENDPOINT)")
	flag.StringVar(&c.public, "public", "", "public endpoint used only for signing; empty skips check h")
	flag.StringVar(&c.region, "region", "us-east-1", "S3 region")
	flag.StringVar(&c.access, "access", os.Getenv("S3_ACCESS_KEY"), "access key (env S3_ACCESS_KEY)")
	flag.StringVar(&c.secret, "secret", os.Getenv("S3_SECRET_KEY"), "secret key (env S3_SECRET_KEY)")
	flag.StringVar(&c.bucket, "bucket", "spike-processing", "processing bucket")
	flag.StringVar(&c.completed, "completed", "spike-completed", "completed bucket")
	flag.StringVar(&c.origin, "origin", "http://localhost:3000", "browser origin for the CORS check")
	flag.IntVar(&c.manyParts, "many-parts", 2000, "parts for the timed complete with tiny parts")
	flag.IntVar(&c.fallbackParts, "fallback-parts", 200, "5 MiB parts when tiny parts are refused")
	flag.Int64Var(&c.copyBytes, "copy-bytes", 0, "object size for the copy check; 0 skips check g")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	s, err := build(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	s.lifecycleOfOneFile(ctx)
	s.manyParts(ctx)
	s.copyLarge(ctx)
	s.publicEndpoint(ctx)
	s.corsFromBrowser(ctx)
	s.bucketChecks(ctx)
	failed := 0
	for _, r := range s.rows {
		fmt.Printf("| %s | %s | %s | %s | %s |\n", c.server, c.sdk, r.check, r.result, r.detail)
		if r.result == "FAIL" {
			failed++
		}
	}
	return min(failed, 1)
}

func build(c config) (*spike, error) {
	base := storeConfig{endpoint: c.endpoint, region: c.region, access: c.access, secret: c.secret, bucket: c.bucket, completed: c.completed}
	variants := map[string]storeConfig{"store": base}
	bad := base
	bad.secret = "wrong-" + base.secret
	variants["bad"] = bad
	down := base
	down.endpoint = "http://127.0.0.1:1"
	variants["down"] = down
	if c.public != "" {
		pub := base
		pub.endpoint = c.public
		variants["public"] = pub
	}
	made := map[string]objectStore{}
	for name, sc := range variants {
		st, err := open(c.sdk, sc)
		if err != nil {
			return nil, err
		}
		made[name] = st
	}
	return &spike{cfg: c, store: made["store"], signer: made["public"], badCreds: made["bad"], down: made["down"]}, nil
}

func open(sdk string, c storeConfig) (objectStore, error) {
	if sdk != "minio" {
		return nil, fmt.Errorf("unknown sdk %q", sdk)
	}
	return newMinioStore(c)
}
