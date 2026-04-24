// Demo seeder: populates the sqlite store with 90 days of synthetic
// checks and a handful of realistic-looking incidents so the UI can be
// driven locally without running real probes.
//
// Usage:
//   go run ./cmd/seed -config config.local.yaml
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/Nobody9512/statuspage/internal/config"
	"github.com/Nobody9512/statuspage/internal/storage"
)

type incidentSpec struct {
	target    string
	startAgo  time.Duration
	duration  time.Duration
	lastError string
	detail    string
}

func main() {
	configPath := flag.String("config", "config.local.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	store, err := storage.Open(cfg.Storage.SQLitePath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	rng := rand.New(rand.NewSource(42))
	now := time.Now()
	start := now.Add(-90 * 24 * time.Hour)

	incidents := defaultIncidents(cfg.Targets, now)
	incidentByTarget := map[string][]incidentSpec{}
	for _, inc := range incidents {
		incidentByTarget[inc.target] = append(incidentByTarget[inc.target], inc)
	}

	total := 0
	for _, t := range cfg.Targets {
		specs := incidentByTarget[t.Name]
		count, err := seedTarget(store, t.Name, start, now, specs, rng)
		if err != nil {
			log.Fatalf("seed %s: %v", t.Name, err)
		}
		total += count
		fmt.Printf("  - %-22s  %5d checks, %d incidents\n", t.Name, count, len(specs))
	}

	for _, inc := range incidents {
		if err := openAndMaybeResolve(store, inc, now); err != nil {
			log.Fatalf("incident %s: %v", inc.target, err)
		}
	}

	fmt.Printf("\nSeeded %d checks and %d incidents into %s\n", total, len(incidents), cfg.Storage.SQLitePath)
	fmt.Println("Start the server with:")
	fmt.Printf("  go run ./cmd/statuspage -config %s\n", *configPath)
}

func seedTarget(store *storage.Store, target string, start, now time.Time, specs []incidentSpec, rng *rand.Rand) (int, error) {
	interval := 15 * time.Minute
	var batch []storage.Check
	for ts := start; !ts.After(now); ts = ts.Add(interval) {
		status := storage.StatusOK
		errText := ""
		detail := ""
		for _, inc := range specs {
			incStart := now.Add(-inc.startAgo)
			incEnd := incStart.Add(inc.duration)
			if !ts.Before(incStart) && ts.Before(incEnd) {
				status = storage.StatusDown
				errText = inc.lastError
				detail = inc.detail
				break
			}
		}
		latency := int64(80 + rng.Intn(220))
		if status == storage.StatusDown {
			latency = int64(1500 + rng.Intn(3000))
		}
		batch = append(batch, storage.Check{
			Target:    target,
			CheckedAt: ts,
			Status:    status,
			LatencyMs: latency,
			Error:     errText,
			Detail:    detail,
		})
	}
	if err := store.SaveChecksBulk(batch); err != nil {
		return 0, err
	}
	return len(batch), nil
}

func openAndMaybeResolve(store *storage.Store, inc incidentSpec, now time.Time) error {
	startedAt := now.Add(-inc.startAgo)
	id, err := store.OpenIncident(inc.target, startedAt, inc.lastError)
	if err != nil {
		return err
	}
	resolvedAt := startedAt.Add(inc.duration)
	if resolvedAt.Before(now) {
		if err := store.ResolveIncident(id, resolvedAt); err != nil {
			return err
		}
	}
	return nil
}

// defaultIncidents builds a set of realistic-looking incidents that mirror
// the kinds of errors the production checker produces. Only targets that
// exist in the config get incidents assigned to them.
func defaultIncidents(targets []config.Target, now time.Time) []incidentSpec {
	have := map[string]bool{}
	for _, t := range targets {
		have[t.Name] = true
	}

	d := 24 * time.Hour
	candidates := []incidentSpec{
		{
			target:    "EDM e-Invoice",
			startAgo:  5 * time.Minute,
			duration:  5 * time.Minute,
			lastError: `Get "http://127.0.0.1/health/edm": context deadline exceeded`,
			detail:    "connect tcp 127.0.0.1:80: i/o timeout after 10s",
		},
		{
			target:    "Currency API",
			startAgo:  2*d + 3*time.Hour,
			duration:  5 * time.Minute,
			lastError: `Get "https://ms-price-service.example.com/v1/api/currencies": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
			detail:    "net/http: timeout after 10s reading response headers",
		},
		{
			target:    "EDM e-Invoice",
			startAgo:  2*d + 15*time.Hour,
			duration:  1*time.Hour + 53*time.Minute,
			lastError: "HTTP 503: EDM authentication failed: Session ID not received",
			detail:    "response body: {\"error\":\"auth-failed\",\"message\":\"Session ID not received\"}",
		},
		{
			target:    "EDM e-Invoice",
			startAgo:  2*d + 20*time.Hour,
			duration:  1*time.Hour + 20*time.Minute,
			lastError: `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Server</faultcode><faultstring>internal</faultstring></s:Fault></s:Body></s:Envelope>`,
			detail:    "SOAP fault returned HTTP 500",
		},
		{
			target:    "Public website",
			startAgo:  2*d + 20*time.Hour,
			duration:  1*time.Hour + 20*time.Minute,
			lastError: "unexpected status 500",
			detail:    "body: <html><body>Whoops — something went wrong (trace-id: 8c2a...)</body></html>",
		},
		{
			target:    "Internal REST API",
			startAgo:  2*d + 20*time.Hour,
			duration:  1*time.Hour + 20*time.Minute,
			lastError: "unexpected status 403",
			detail:    `{"error":"forbidden","hint":"token expired"}`,
		},
		{
			target:    "Laravel queue",
			startAgo:  2*d + 21*time.Hour,
			duration:  1*time.Hour + 24*time.Minute,
			lastError: "failed_jobs 5429 > threshold 10",
			detail:    "reserved older than 15m: 142 rows",
		},
		{
			target:    "Primary Postgres",
			startAgo:  12 * d,
			duration:  18 * time.Minute,
			lastError: `ping: dial tcp 10.0.0.12:5432: connect: connection refused`,
			detail:    "driver returned: pq: connection refused",
		},
		{
			target:    "Redis",
			startAgo:  20 * d,
			duration:  7 * time.Minute,
			lastError: "dial tcp 10.0.0.15:6379: i/o timeout",
			detail:    "after 3 retries",
		},
		{
			target:    "SMTP port",
			startAgo:  40 * d,
			duration:  2 * time.Hour,
			lastError: "dial tcp 10.0.0.25:25: connect: connection refused",
			detail:    "tcp probe",
		},
	}

	out := make([]incidentSpec, 0, len(candidates))
	for _, c := range candidates {
		if have[c.target] {
			out = append(out, c)
		}
	}
	_ = now
	return out
}
