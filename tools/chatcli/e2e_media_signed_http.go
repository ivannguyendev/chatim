package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

var signedClient = &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{ExpectContinueTimeout: 5 * time.Second}}

func putSigned(ctx context.Context, url string, body []byte) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Expect", "100-continue")
	resp, err := signedClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Header.Get("ETag"), nil
}

type signedDownload struct {
	status int
	body   []byte
	header http.Header
}

func getSigned(ctx context.Context, url string) (signedDownload, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return signedDownload{}, err
	}
	resp, err := signedClient.Do(req)
	if err != nil {
		return signedDownload{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return signedDownload{}, err
	}
	return signedDownload{status: resp.StatusCode, body: body, header: resp.Header}, nil
}
