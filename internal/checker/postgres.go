package checker

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/Nobody9512/statuspage/internal/config"
)

type postgresChecker struct {
	t       config.Target
	timeout time.Duration
}

func newPostgresChecker(t config.Target, def time.Duration) *postgresChecker {
	return &postgresChecker{t: t, timeout: t.Timeout(def)}
}

func (c *postgresChecker) Name() string { return c.t.Name }

func (c *postgresChecker) Check(ctx context.Context) Result {
	return measure(func() (bool, string, string) {
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		conn, err := pgx.Connect(reqCtx, c.t.DSN)
		if err != nil {
			return false, "", err.Error()
		}
		defer conn.Close(reqCtx)
		var one int
		if err := conn.QueryRow(reqCtx, "SELECT 1").Scan(&one); err != nil {
			return false, "", err.Error()
		}
		return true, "SELECT 1 ok", ""
	})
}
