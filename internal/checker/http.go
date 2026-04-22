package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Nobody9512/statuspage/internal/config"
)

type httpChecker struct {
	t       config.Target
	client  *http.Client
	timeout time.Duration
}

func newHTTPChecker(t config.Target, def time.Duration) *httpChecker {
	timeout := t.Timeout(def)
	return &httpChecker{
		t:       t,
		timeout: timeout,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *httpChecker) Name() string { return c.t.Name }

func (c *httpChecker) Check(ctx context.Context) Result {
	return measure(func() (bool, string, string) {
		method := c.t.Method
		if method == "" {
			method = "GET"
		}
		var body io.Reader
		if c.t.Body != "" {
			body = strings.NewReader(c.t.Body)
		}
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, method, c.t.URL, body)
		if err != nil {
			return false, "", fmt.Sprintf("build request: %v", err)
		}
		for k, v := range c.t.Headers {
			if strings.EqualFold(k, "Host") {
				req.Host = v
				continue
			}
			req.Header.Set(k, v)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return false, "", err.Error()
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		if accepted(c.t.ExpectStatus, resp.StatusCode) {
			return true, fmt.Sprintf("HTTP %d", resp.StatusCode), ""
		}
		errMsg := fmt.Sprintf("unexpected status %d", resp.StatusCode)
		if detail := extractErrorBody(bodyBytes); detail != "" {
			errMsg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, detail)
		}
		return false, fmt.Sprintf("HTTP %d", resp.StatusCode), errMsg
	})
}

// extractErrorBody tries to find a human-readable error message in the
// response body. If the body is JSON with an "error" (or "message") field,
// that value is returned. Otherwise a trimmed plain-text snippet is used.
func extractErrorBody(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "{") {
		var payload map[string]any
		if err := json.Unmarshal([]byte(trimmed), &payload); err == nil {
			for _, key := range []string{"error", "message", "detail", "err"} {
				if v, ok := payload[key]; ok {
					if s, ok := v.(string); ok && s != "" {
						return s
					}
				}
			}
		}
	}
	// Strip HTML-ish content and long lines to avoid noisy error text.
	if strings.Contains(trimmed, "<html") || strings.Contains(trimmed, "<!DOCTYPE") {
		return ""
	}
	if len(trimmed) > 200 {
		trimmed = trimmed[:200] + "..."
	}
	return trimmed
}

func accepted(list []int, code int) bool {
	if len(list) == 0 {
		return code >= 200 && code < 400
	}
	for _, x := range list {
		if x == code {
			return true
		}
	}
	return false
}
