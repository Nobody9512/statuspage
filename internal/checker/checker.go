package checker

import (
	"context"
	"fmt"
	"time"

	"github.com/Nobody9512/statuspage/internal/config"
)

type Result struct {
	OK        bool
	LatencyMs int64
	Error     string
	Detail    string
}

type Checker interface {
	Name() string
	Check(ctx context.Context) Result
}

func Build(t config.Target, defaultTimeout time.Duration) (Checker, error) {
	switch t.Type {
	case "http":
		return newHTTPChecker(t, defaultTimeout), nil
	case "soap":
		return newSOAPChecker(t, defaultTimeout), nil
	case "postgres":
		return newPostgresChecker(t, defaultTimeout), nil
	case "redis":
		return newRedisChecker(t, defaultTimeout), nil
	case "laravel_jobs":
		return newJobsChecker(t, defaultTimeout), nil
	case "tcp":
		return newTCPChecker(t, defaultTimeout), nil
	default:
		return nil, fmt.Errorf("unknown checker type: %s", t.Type)
	}
}

func measure(fn func() (bool, string, string)) Result {
	start := time.Now()
	ok, detail, errStr := fn()
	return Result{
		OK:        ok,
		LatencyMs: time.Since(start).Milliseconds(),
		Detail:    detail,
		Error:     errStr,
	}
}
