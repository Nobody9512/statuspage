package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Nobody9512/statuspage/internal/config"
	"github.com/Nobody9512/statuspage/internal/notifier"
	"github.com/Nobody9512/statuspage/internal/scheduler"
	"github.com/Nobody9512/statuspage/internal/storage"
	"github.com/Nobody9512/statuspage/internal/web"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config.yaml")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	store, err := storage.Open(cfg.Storage.SQLitePath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	notif := notifier.New(cfg.Notifier, store)
	if notif.Enabled() {
		log.Printf("notifier: enabled, threshold=%dm, to=%v",
			cfg.Notifier.DownThresholdMinutes, cfg.Notifier.SMTP.To)
	} else {
		log.Printf("notifier: disabled")
	}

	sched, err := scheduler.New(cfg.Scheduler, cfg.Targets, store, notif)
	if err != nil {
		log.Fatalf("scheduler: %v", err)
	}

	server, err := web.New(cfg.Server, cfg.Server.AutoRefreshSeconds, cfg.Targets, store)
	if err != nil {
		log.Fatalf("web: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	httpServer := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("scheduler: interval=%dm retention=%dd targets=%d",
			cfg.Scheduler.IntervalMinutes, cfg.Scheduler.RetentionDays, len(cfg.Targets))
		wrapped := &wrappedScheduler{sched: sched, server: server}
		wrapped.Run(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("web: listening on http://%s", cfg.Server.Listen)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("http: %v", err)
		}
	}()

	<-sigCh
	log.Printf("shutting down...")
	cancel()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = httpServer.Shutdown(shutdownCtx)
	wg.Wait()
}

// wrappedScheduler ties scheduler ticks to web "last cycle" label.
type wrappedScheduler struct {
	sched  *scheduler.Scheduler
	server *web.Server
}

func (w *wrappedScheduler) Run(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		w.sched.Run(ctx)
		close(done)
	}()

	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			<-done
			return
		case <-tick.C:
			w.server.SetLastCycle(time.Now())
		case <-done:
			return
		}
	}
}
