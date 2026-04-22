package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Nobody9512/statuspage/internal/checker"
	"github.com/Nobody9512/statuspage/internal/config"
	"github.com/Nobody9512/statuspage/internal/notifier"
	"github.com/Nobody9512/statuspage/internal/storage"
)

type Scheduler struct {
	cfg      config.SchedulerConfig
	checkers []checker.Checker
	store    *storage.Store
	notifier *notifier.Notifier
}

func New(cfg config.SchedulerConfig, targets []config.Target, store *storage.Store, n *notifier.Notifier) (*Scheduler, error) {
	defaultTimeout := time.Duration(cfg.CheckTimeoutSeconds) * time.Second
	var list []checker.Checker
	for _, t := range targets {
		c, err := checker.Build(t, defaultTimeout)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return &Scheduler{cfg: cfg, checkers: list, store: store, notifier: n}, nil
}

func (s *Scheduler) Run(ctx context.Context) {
	s.runOnce(ctx)

	interval := time.Duration(s.cfg.IntervalMinutes) * time.Minute
	checkTicker := time.NewTicker(interval)
	defer checkTicker.Stop()

	cleanupTicker := time.NewTicker(24 * time.Hour)
	defer cleanupTicker.Stop()

	emailTicker := time.NewTicker(time.Minute)
	defer emailTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-checkTicker.C:
			s.runOnce(ctx)
		case <-emailTicker.C:
			s.notifier.ProcessPendingDown(ctx)
		case <-cleanupTicker.C:
			s.cleanup()
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context) {
	start := time.Now()
	var wg sync.WaitGroup
	for _, c := range s.checkers {
		wg.Add(1)
		go func(c checker.Checker) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("checker %s panic: %v", c.Name(), r)
				}
			}()
			res := c.Check(ctx)
			s.handleResult(c.Name(), res)
		}(c)
	}
	wg.Wait()
	log.Printf("cycle complete: %d checks in %s", len(s.checkers), time.Since(start).Round(time.Millisecond))
	s.notifier.ProcessPendingDown(ctx)
}

func (s *Scheduler) handleResult(target string, res checker.Result) {
	now := time.Now()
	status := storage.StatusOK
	if !res.OK {
		status = storage.StatusDown
	}
	if err := s.store.SaveCheck(storage.Check{
		Target:    target,
		CheckedAt: now,
		Status:    status,
		LatencyMs: res.LatencyMs,
		Error:     res.Error,
		Detail:    res.Detail,
	}); err != nil {
		log.Printf("save check %s: %v", target, err)
	}

	open, err := s.store.GetOpenIncident(target)
	if err != nil {
		log.Printf("get open incident %s: %v", target, err)
		return
	}

	if !res.OK {
		if open == nil {
			if _, err := s.store.OpenIncident(target, now, res.Error); err != nil {
				log.Printf("open incident %s: %v", target, err)
			}
		} else {
			if res.Error != "" && res.Error != open.LastError {
				_ = s.store.UpdateIncidentError(open.ID, res.Error)
			}
		}
		return
	}

	if open != nil {
		if err := s.store.ResolveIncident(open.ID, now); err != nil {
			log.Printf("resolve incident %s: %v", target, err)
			return
		}
		resolved := now
		open.ResolvedAt = &resolved
		s.notifier.NotifyRecovered(*open)
	}
}

func (s *Scheduler) cleanup() {
	retention := time.Duration(s.cfg.RetentionDays) * 24 * time.Hour
	dc, di, err := s.store.Cleanup(retention)
	if err != nil {
		log.Printf("cleanup: %v", err)
		return
	}
	log.Printf("cleanup: removed %d checks, %d incidents", dc, di)
}
