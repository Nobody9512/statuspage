package checker

import (
	"context"
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
		_, _ = io.Copy(io.Discard, resp.Body)
		if accepted(c.t.ExpectStatus, resp.StatusCode) {
			return true, fmt.Sprintf("HTTP %d", resp.StatusCode), ""
		}
		return false, fmt.Sprintf("HTTP %d", resp.StatusCode),
			fmt.Sprintf("unexpected status %d", resp.StatusCode)
	})
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
