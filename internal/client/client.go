package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL  = "https://api.turso.tech"
	defaultAttempts = 5
	maxBodyBytes    = 2 << 20
)

var ErrNotFound = errors.New("turso object not found")

type APIError struct {
	StatusCode int
	Method     string
	Path       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Turso API request failed: %s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
}

type Client struct {
	baseURL      *url.URL
	token        string
	organization string
	userAgent    string
	httpClient   *http.Client
	maxAttempts  int
	baseDelay    time.Duration
}

func New(baseURL, token, organization, version string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("invalid Turso API URL")
	}
	if u.User != nil {
		return nil, errors.New("Turso API URL must not contain credentials")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return nil, errors.New("Turso API URL must use HTTPS (HTTP is allowed only for localhost tests)")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("Turso API token is required")
	}
	if strings.TrimSpace(organization) == "" {
		return nil, errors.New("Turso organization is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if version == "" {
		version = "dev"
	}
	return &Client{
		baseURL:      u,
		token:        token,
		organization: organization,
		userAgent:    "terraform-provider-turso/" + version,
		httpClient:   httpClient,
		maxAttempts:  defaultAttempts,
		baseDelay:    250 * time.Millisecond,
	}, nil
}

func (c *Client) Organization() string { return c.organization }

func (c *Client) request(ctx context.Context, method, path string, requestBody, responseBody any) error {
	var encoded []byte
	var err error
	if requestBody != nil {
		encoded, err = json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode Turso request: %w", err)
		}
	}

	attempts := 1
	if method == http.MethodGet || method == http.MethodPatch || method == http.MethodDelete {
		attempts = c.maxAttempts
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		u := c.baseURL.ResolveReference(&url.URL{Path: path})
		req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(encoded))
		if err != nil {
			return fmt.Errorf("create Turso request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		if requestBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt+1 < attempts && ctx.Err() == nil {
				if err := wait(ctx, c.delay(attempt, "")); err != nil {
					return err
				}
				continue
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("Turso API transport failure: %s", strings.ReplaceAll(err.Error(), c.token, "[REDACTED]"))
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read Turso response: %w", readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close Turso response: %w", closeErr)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if responseBody == nil || len(bytes.TrimSpace(body)) == 0 {
				return nil
			}
			if err := json.Unmarshal(body, responseBody); err != nil {
				return fmt.Errorf("decode Turso response: %w", err)
			}
			return nil
		}

		if resp.StatusCode == http.StatusNotFound {
			return ErrNotFound
		}
		if attempt+1 < attempts && retryableStatus(resp.StatusCode) {
			if err := wait(ctx, c.delay(attempt, resp.Header.Get("Retry-After"))); err != nil {
				return err
			}
			continue
		}
		return &APIError{StatusCode: resp.StatusCode, Method: method, Path: path}
	}
	return errors.New("Turso API retry budget exhausted")
}

func (c *Client) delay(attempt int, retryAfter string) time.Duration {
	retryAfter = strings.TrimSpace(retryAfter)
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		d := time.Duration(seconds) * time.Second
		if d > 10*time.Second {
			return 10 * time.Second
		}
		return d
	}
	if when, err := http.ParseTime(retryAfter); err == nil {
		d := time.Until(when)
		if d < 0 {
			return 0
		}
		if d > 10*time.Second {
			return 10 * time.Second
		}
		return d
	}
	d := c.baseDelay * time.Duration(1<<attempt)
	if d > 4*time.Second {
		return 4 * time.Second
	}
	return d
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusInternalServerError ||
		status == http.StatusBadGateway || status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func orgPath(org string, parts ...string) string {
	segments := []string{"", "v1", "organizations", url.PathEscape(org)}
	for _, part := range parts {
		segments = append(segments, url.PathEscape(part))
	}
	return strings.Join(segments, "/")
}
