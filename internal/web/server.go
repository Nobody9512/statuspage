package web

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Nobody9512/statuspage/internal/config"
	"github.com/Nobody9512/statuspage/internal/storage"
)

//go:embed templates/*.html
var templatesFS embed.FS

const windowDays = 90

type Server struct {
	cfg       config.ServerConfig
	refresh   int
	targets   []config.Target
	targetMap map[string]config.Target
	store     *storage.Store
	tmpl      *template.Template
	lastCycle atomic.Value
}

func New(cfg config.ServerConfig, refreshSeconds int, targets []config.Target, store *storage.Store) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	tm := map[string]config.Target{}
	for _, t := range targets {
		tm[t.Name] = t
	}
	s := &Server{
		cfg: cfg, refresh: refreshSeconds,
		targets: targets, targetMap: tm,
		store: store, tmpl: tmpl,
	}
	s.lastCycle.Store(time.Time{})
	return s, nil
}

func (s *Server) SetLastCycle(t time.Time) { s.lastCycle.Store(t) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/service/", s.handleService)
	mux.HandleFunc("/api/checks/", s.handleAPIChecks)
	return s.basicAuth(mux)
}

func (s *Server) basicAuth(next http.Handler) http.Handler {
	if s.cfg.BasicAuth.User == "" && s.cfg.BasicAuth.Password == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(s.cfg.BasicAuth.User)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(s.cfg.BasicAuth.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="ERP Monitor"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- view models --------------------------------------------------

type dayCell struct {
	Class        string
	DateLabel    string
	Summary      string
	RelatedTitle string
}

type serviceCard struct {
	Name         string
	NameEscaped  string
	StatusClass  string
	StatusLabel  string
	BannerClass  string
	UptimeLabel  string
	Days         []dayCell
}

type incidentEntry struct {
	Title         string
	Resolved      bool
	LastError     string
	StartedLabel  string
	ResolvedLabel string
}

type pastDay struct {
	DateLabel string
	IsToday   bool
	Incidents []incidentEntry
}

type banner struct {
	Class string
	Text  string
}

type dashboardData struct {
	Title       string
	Brand       string
	AutoRefresh int
	LastCycle   string
	Window      int
	Banner      banner
	Services    []serviceCard
	PastDays    []pastDay
}

type serviceData struct {
	Title       string
	Brand       string
	AutoRefresh int
	LastCycle   string
	Window      int
	Service     serviceCard
	PastDays    []pastDay
}

// --- handlers -----------------------------------------------------

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	now := time.Now()
	since := now.Add(-windowDays * 24 * time.Hour)
	since7d := now.Add(-7 * 24 * time.Hour)

	cards := make([]serviceCard, 0, len(s.targets))
	anyDown := 0
	anyWarn := 0
	for _, t := range s.targets {
		card := s.buildCard(t, since, now)
		if card.StatusClass == "down" {
			anyDown++
		} else if card.StatusClass == "warn" {
			anyWarn++
		}
		cards = append(cards, card)
	}
	sort.Slice(cards, func(i, j int) bool {
		order := map[string]int{"down": 0, "warn": 1, "unknown": 2, "ok": 3}
		if order[cards[i].StatusClass] != order[cards[j].StatusClass] {
			return order[cards[i].StatusClass] < order[cards[j].StatusClass]
		}
		return cards[i].Name < cards[j].Name
	})

	b := computeBanner(anyDown, anyWarn, len(cards))

	past, _ := s.buildPastDays("", since7d, now)

	s.render(w, "dashboard.html", dashboardData{
		Title:       "Dashboard",
		Brand:       s.cfg.BrandName,
		AutoRefresh: s.refresh,
		LastCycle:   s.lastCycleLabel(),
		Window:      windowDays,
		Banner:      b,
		Services:    cards,
		PastDays:    past,
	})
}

func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/service/")
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	t, ok := s.targetMap[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	now := time.Now()
	since := now.Add(-windowDays * 24 * time.Hour)

	card := s.buildCard(t, since, now)
	past, _ := s.buildPastDays(name, since, now)

	s.render(w, "service.html", serviceData{
		Title:       name,
		Brand:       s.cfg.BrandName,
		AutoRefresh: s.refresh,
		LastCycle:   s.lastCycleLabel(),
		Window:      windowDays,
		Service:     card,
		PastDays:    past,
	})
}

func (s *Server) handleAPIChecks(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/checks/")
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	if _, ok := s.targetMap[name]; !ok {
		http.NotFound(w, r)
		return
	}
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		fmt.Sscanf(v, "%d", &days)
		if days < 1 {
			days = 1
		}
		if days > windowDays {
			days = windowDays
		}
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	checks, err := s.store.ListChecks(name, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(checks)
}

// --- builders -----------------------------------------------------

func (s *Server) buildCard(t config.Target, since, until time.Time) serviceCard {
	latest, _ := s.store.LatestCheck(t.Name)
	uptime, total, _ := s.store.UptimePercent(t.Name, since)
	rollup, _ := s.store.DailyRollup(t.Name, since, until)
	incidents, _ := s.store.TargetIncidents(t.Name, since)

	incByDay := groupIncidentsByDay(incidents)

	days := make([]dayCell, len(rollup))
	for i, r := range rollup {
		cell := dayCell{DateLabel: r.Date.Format("2 Jan 2006")}
		switch {
		case r.Total == 0:
			cell.Class = "none"
			cell.Summary = "No data recorded on this day."
		case r.Down == 0:
			cell.Class = "ok"
			cell.Summary = "No downtime recorded on this day."
		default:
			downPct := float64(r.Down) / float64(r.Total) * 100.0
			if downPct < 5.0 {
				cell.Class = "warn"
			} else {
				cell.Class = "down"
			}
			cell.Summary = fmt.Sprintf("%d of %d checks failed (%.1f%% down).", r.Down, r.Total, downPct)
		}
		if list, ok := incByDay[r.Date.Format("2006-01-02")]; ok && len(list) > 0 {
			cell.RelatedTitle = incidentTitle(list[0])
		}
		days[i] = cell
	}

	card := serviceCard{
		Name:        t.Name,
		NameEscaped: url.PathEscape(t.Name),
		Days:        days,
	}

	if total == 0 {
		card.UptimeLabel = "— uptime"
	} else {
		card.UptimeLabel = fmt.Sprintf("%.2f %% uptime", uptime)
	}
	if latest == nil {
		card.StatusClass = "unknown"
		card.StatusLabel = "No data"
		card.BannerClass = "warn"
		return card
	}
	if latest.Status == storage.StatusOK {
		card.StatusClass = "ok"
		card.StatusLabel = "Operational"
		card.BannerClass = ""
	} else {
		card.StatusClass = "down"
		card.StatusLabel = "Major Outage"
		card.BannerClass = "down"
	}
	return card
}

func (s *Server) buildPastDays(target string, since, until time.Time) ([]pastDay, error) {
	var incidents []storage.Incident
	var err error
	if target == "" {
		incidents, err = s.store.RecentIncidents(since)
	} else {
		incidents, err = s.store.TargetIncidents(target, since)
	}
	if err != nil {
		return nil, err
	}
	byDay := groupIncidentsByDay(incidents)

	startDay := time.Date(until.Year(), until.Month(), until.Day(), 0, 0, 0, 0, until.Location())
	days := []pastDay{}
	for d := startDay; !d.Before(since); d = d.Add(-24 * time.Hour) {
		key := d.Format("2006-01-02")
		block := pastDay{
			DateLabel: d.Format("Jan 2, 2006"),
			IsToday:   d.Equal(startDay),
		}
		if list, ok := byDay[key]; ok {
			for _, inc := range list {
				block.Incidents = append(block.Incidents, incidentEntryFrom(inc, target == ""))
			}
		}
		days = append(days, block)
	}
	return days, nil
}

func groupIncidentsByDay(list []storage.Incident) map[string][]storage.Incident {
	out := map[string][]storage.Incident{}
	for _, inc := range list {
		key := inc.StartedAt.Format("2006-01-02")
		out[key] = append(out[key], inc)
	}
	return out
}

func incidentEntryFrom(inc storage.Incident, includeTarget bool) incidentEntry {
	entry := incidentEntry{
		Title:        incidentTitle(inc),
		Resolved:     inc.ResolvedAt != nil,
		LastError:    inc.LastError,
		StartedLabel: inc.StartedAt.UTC().Format("Jan 2, 15:04 UTC"),
	}
	if inc.ResolvedAt != nil {
		entry.ResolvedLabel = inc.ResolvedAt.UTC().Format("Jan 2, 15:04 UTC")
	}
	_ = includeTarget
	return entry
}

func incidentTitle(inc storage.Incident) string {
	if inc.LastError == "" {
		return fmt.Sprintf("%s outage", inc.Target)
	}
	msg := inc.LastError
	if len(msg) > 80 {
		msg = msg[:80] + "..."
	}
	return fmt.Sprintf("%s: %s", inc.Target, msg)
}

func computeBanner(down, warn, total int) banner {
	switch {
	case down == 0 && warn == 0:
		return banner{Class: "", Text: "All Systems Operational"}
	case down == 0:
		return banner{Class: "warn", Text: "Partial Degradation"}
	case down == 1:
		return banner{Class: "down", Text: "One System Down"}
	case down >= total:
		return banner{Class: "down", Text: "Major Outage — all systems affected"}
	default:
		return banner{Class: "down", Text: fmt.Sprintf("%d systems down", down)}
	}
}

// --- plumbing -----------------------------------------------------

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, err := s.tmpl.Clone()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tmpl.ParseFS(templatesFS, "templates/"+page, "templates/layout.html"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("render: %v", err)
	}
}

func (s *Server) lastCycleLabel() string {
	if v := s.lastCycle.Load(); v != nil {
		if t, ok := v.(time.Time); ok && !t.IsZero() {
			return humanAgo(t)
		}
	}
	return "—"
}

func humanAgo(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return t.Format("Jan 2 15:04")
}
