package checker

import (
	"context"
	"net"
	"time"

	"github.com/Nobody9512/statuspage/internal/config"
)

type tcpChecker struct {
	t       config.Target
	timeout time.Duration
}

func newTCPChecker(t config.Target, def time.Duration) *tcpChecker {
	return &tcpChecker{t: t, timeout: t.Timeout(def)}
}

func (c *tcpChecker) Name() string { return c.t.Name }

func (c *tcpChecker) Check(ctx context.Context) Result {
	return measure(func() (bool, string, string) {
		d := net.Dialer{Timeout: c.timeout}
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		conn, err := d.DialContext(reqCtx, "tcp", c.t.Addr)
		if err != nil {
			return false, "", err.Error()
		}
		_ = conn.Close()
		return true, "connected", ""
	})
}
