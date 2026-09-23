// Package google adapts Google Maps Platform (Places API New and Routes API) to the place and
// routing ports. It talks plain HTTPS+JSON; the API key travels in a header and is never logged.
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultPlacesBase = "https://places.googleapis.com"
	defaultRoutesBase = "https://routes.googleapis.com"
	maxResponseBytes  = 4 << 20
)

type client struct {
	apiKey string
	http   *http.Client
	base   string
}

func newClient(apiKey, base string, httpClient *http.Client) client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return client{apiKey: apiKey, http: httpClient, base: base}
}

func (c client) do(ctx context.Context, method, path, fieldMask string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Goog-Api-Key", c.apiKey)
	req.Header.Set("X-Goog-FieldMask", fieldMask)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", stripURL(err))
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("google responded %d: %s", resp.StatusCode, summary(payload))
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// stripURL drops the request URL from transport errors so nothing sensitive reaches the logs.
func stripURL(err error) error {
	if urlErr, ok := err.(interface{ Unwrap() error }); ok && urlErr.Unwrap() != nil {
		return urlErr.Unwrap()
	}
	return err
}

func summary(payload []byte) string {
	var e struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &e) == nil && e.Error.Status != "" {
		return e.Error.Status + ": " + e.Error.Message
	}
	return "unexpected response"
}
