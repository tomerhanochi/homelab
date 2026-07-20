package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// env returns the environment variable, or def if unset/empty.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// mustEnv returns the environment variable or exits with a clear error.
func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", key)
	}
	return v
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// request performs an HTTP request with an optional JSON body. body may be nil.
// headers are applied verbatim. It returns the status code and response bytes;
// it does not treat non-2xx as an error so callers can branch on the status.
func request(method, url string, headers map[string]string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, reader)
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

	resp, err := httpClient.Do(req)
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

// waitReady polls url with GET until it returns any HTTP response (2xx-5xx),
// meaning the app's web server is up, or the deadline elapses.
func waitReady(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, _, err := request(http.MethodGet, url, nil, nil)
		if err == nil && status < 500 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s (last err: %v)", url, err)
		}
		log.Printf("waiting for %s ...", url)
		time.Sleep(5 * time.Second)
	}
}
