package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL     string
	HTTPClient  *http.Client
	RetryDelay  time.Duration
	MaxAttempts int
}

type ExchangeError struct {
	Exchange   string
	StatusCode int
	Code       string
	Message    string
}

func (e *ExchangeError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s error %s: %s", e.Exchange, e.Code, e.Message)
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s http status %d: %s", e.Exchange, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s error: %s", e.Exchange, e.Message)
}

type ResponseMeta struct {
	Headers map[string]string
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		RetryDelay:  100 * time.Millisecond,
		MaxAttempts: 3,
	}
}

func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, out any) (ResponseMeta, error) {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = 100 * time.Millisecond
	}

	endpoint := c.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 1; attempt <= c.MaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return ResponseMeta{}, err
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			meta, err := decodeResponse(resp, out)
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			if err == nil {
				return meta, nil
			}
			lastErr = err
			if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return meta, err
			}
		}

		if attempt < c.MaxAttempts {
			select {
			case <-ctx.Done():
				return ResponseMeta{}, ctx.Err()
			case <-time.After(c.RetryDelay * time.Duration(1<<(attempt-1))):
			}
		}
	}

	return ResponseMeta{}, lastErr
}

func decodeResponse(resp *http.Response, out any) (ResponseMeta, error) {
	meta := ResponseMeta{Headers: map[string]string{}}
	for key, values := range resp.Header {
		if len(values) > 0 {
			meta.Headers[key] = values[0]
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return meta, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return meta, &ExchangeError{
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(body)),
		}
	}
	if len(body) == 0 {
		return meta, nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return meta, err
	}
	return meta, nil
}

func HeaderSubset(headers map[string]string, names ...string) map[string]string {
	out := make(map[string]string)
	for _, name := range names {
		for key, value := range headers {
			if strings.EqualFold(key, name) {
				out[name] = value
				break
			}
		}
	}
	return out
}
