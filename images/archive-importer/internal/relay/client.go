// Package relay posts notification events to the recognizer
// notification-relay HTTP endpoint with bounded retries.
package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	url     string
	max     int
	backoff time.Duration
	http    *http.Client
}

func NewClient(url string, maxRetries int, backoff time.Duration) *Client {
	return &Client{
		url:     url,
		max:     maxRetries,
		backoff: backoff,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Post sends event to the relay. Returns nil on first 2xx response or
// after retrying up to maxRetries times on 5xx / network errors.
func (c *Client) Post(event any) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var lastErr error
	for i := 0; i <= c.max; i++ {
		req, err := http.NewRequest("POST", c.url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
		} else {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			if resp.StatusCode < 500 {
				if resp.StatusCode >= 400 {
					return fmt.Errorf("relay returned %d: %s (event body: %s)", resp.StatusCode, string(respBody), string(body))
				}
				return nil
			}
			lastErr = fmt.Errorf("relay returned %d: %s", resp.StatusCode, string(respBody))
		}
		time.Sleep(c.backoff * time.Duration(1<<i))
	}
	return fmt.Errorf("relay POST exhausted retries: %w", lastErr)
}
