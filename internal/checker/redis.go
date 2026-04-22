package checker

import (
	"context"
	"time"

	"github.com/Nobody9512/statuspage/internal/config"
	"github.com/redis/go-redis/v9"
)

type redisChecker struct {
	t       config.Target
	timeout time.Duration
}

func newRedisChecker(t config.Target, def time.Duration) *redisChecker {
	return &redisChecker{t: t, timeout: t.Timeout(def)}
}

func (c *redisChecker) Name() string { return c.t.Name }

func (c *redisChecker) Check(ctx context.Context) Result {
	return measure(func() (bool, string, string) {
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		client := redis.NewClient(&redis.Options{
			Addr:        c.t.Addr,
			Password:    c.t.Password,
			DB:          c.t.DB,
			DialTimeout: c.timeout,
			ReadTimeout: c.timeout,
		})
		defer client.Close()
		pong, err := client.Ping(reqCtx).Result()
		if err != nil {
			return false, "", err.Error()
		}
		return true, pong, ""
	})
}
