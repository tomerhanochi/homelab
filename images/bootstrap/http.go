package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// request performs an HTTP request with an optional JSON body. body may be nil.
// headers are applied verbatim. It returns the status code and response bytes;
// it does not treat non-2xx as an error so callers can branch on the status.
func request(ctx context.Context, method, url string, headers map[string]string, body any) (int, []byte, error) {
	return requestWith(ctx, httpClient, method, url, headers, body)
}

// requestWith is request against a specific client (e.g. one carrying a cookie
// jar for qBittorrent's session).
func requestWith(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

// ok reports whether status is a 2xx.
func ok(status int) bool { return status >= 200 && status < 300 }

// waitReady polls url with GET until it returns any HTTP response below 500
// (the app's web server is up) or the timeout/context elapses.
func waitReady(ctx context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		status, _, err := request(ctx, http.MethodGet, url, nil, nil)
		if err == nil && status < 500 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for %s (last err: %v): %w", url, err, ctx.Err())
		case <-time.After(5 * time.Second):
			log.Printf("waiting for %s ...", url)
		}
	}
}

// jsonUnmarshal is encoding/json.Unmarshal, re-exported so the per-app files can
// decode responses without each importing encoding/json.
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// jsonEqual reports whether a and b marshal to the same JSON, so managed fields
// can be compared regardless of type (e.g. a []string desired value vs a []any
// decoded from a server response). Slices are not comparable with ==.
func jsonEqual(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}
