package checker

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/Nobody9512/statuspage/internal/config"
)

type jobsChecker struct {
	t       config.Target
	timeout time.Duration
}

func newJobsChecker(t config.Target, def time.Duration) *jobsChecker {
	return &jobsChecker{t: t, timeout: t.Timeout(def)}
}

func (c *jobsChecker) Name() string { return c.t.Name }

func (c *jobsChecker) Check(ctx context.Context) Result {
	return measure(func() (bool, string, string) {
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		conn, err := pgx.Connect(reqCtx, c.t.DSN)
		if err != nil {
			return false, "", err.Error()
		}
		defer conn.Close(reqCtx)

		var failed int
		if err := conn.QueryRow(reqCtx, `SELECT COUNT(*) FROM failed_jobs`).Scan(&failed); err != nil {
			return false, "", fmt.Sprintf("failed_jobs query: %v", err)
		}

		var stale int
		if c.t.StaleReservedMinutes > 0 {
			cutoff := time.Now().Add(-time.Duration(c.t.StaleReservedMinutes) * time.Minute).Unix()
			if err := conn.QueryRow(reqCtx,
				`SELECT COUNT(*) FROM jobs WHERE reserved_at IS NOT NULL AND reserved_at < $1`, cutoff,
			).Scan(&stale); err != nil {
				return false, "", fmt.Sprintf("stale jobs query: %v", err)
			}
		}

		var pending int
		_ = conn.QueryRow(reqCtx, `SELECT COUNT(*) FROM jobs`).Scan(&pending)

		detail := fmt.Sprintf("failed=%d pending=%d stale=%d", failed, pending, stale)
		threshold := c.t.FailThreshold
		if threshold == 0 {
			threshold = 10
		}
		if failed > threshold {
			return false, detail, fmt.Sprintf("failed_jobs %d > threshold %d", failed, threshold)
		}
		if stale > 0 {
			return false, detail, fmt.Sprintf("%d stale reserved jobs", stale)
		}
		return true, detail, ""
	})
}
