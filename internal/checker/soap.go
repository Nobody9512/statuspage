package checker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Nobody9512/statuspage/internal/config"
)

const defaultSOAPEnvelope = `<?xml version="1.0" encoding="UTF-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body/>
</soap:Envelope>`

type soapChecker struct {
	t       config.Target
	client  *http.Client
	timeout time.Duration
}

func newSOAPChecker(t config.Target, def time.Duration) *soapChecker {
	timeout := t.Timeout(def)
	return &soapChecker{
		t:       t,
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
	}
}

func (c *soapChecker) Name() string { return c.t.Name }

func (c *soapChecker) Check(ctx context.Context) Result {
	return measure(func() (bool, string, string) {
		envelope := c.t.SOAPEnvelope
		if envelope == "" {
			envelope = defaultSOAPEnvelope
		}
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, "POST", c.t.URL, strings.NewReader(envelope))
		if err != nil {
			return false, "", fmt.Sprintf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "text/xml; charset=utf-8")
		if c.t.SOAPAction != "" {
			req.Header.Set("SOAPAction", c.t.SOAPAction)
		}
		for k, v := range c.t.Headers {
			req.Header.Set(k, v)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return false, "", err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode >= 500 {
			return false, fmt.Sprintf("HTTP %d", resp.StatusCode), truncate(string(body), 200)
		}
		if bytes.Contains(body, []byte("<soap:Fault")) || bytes.Contains(body, []byte("<SOAP-ENV:Fault")) {
			return false, "soap fault", truncate(string(body), 200)
		}
		return true, fmt.Sprintf("HTTP %d", resp.StatusCode), ""
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
