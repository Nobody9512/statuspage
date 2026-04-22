package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Status string

const (
	StatusOK   Status = "ok"
	StatusDown Status = "down"
)

type Check struct {
	ID        int64
	Target    string
	CheckedAt time.Time
	Status    Status
	LatencyMs int64
	Error     string
	Detail    string
}

type Incident struct {
	ID                   int64
	Target               string
	StartedAt            time.Time
	ResolvedAt           *time.Time
	LastError            string
	DownEmailSent        bool
	RecoveredEmailSent   bool
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			target TEXT NOT NULL,
			checked_at INTEGER NOT NULL,
			status TEXT NOT NULL,
			latency_ms INTEGER,
			error TEXT,
			detail TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_checks_target_time ON checks(target, checked_at DESC)`,
		`CREATE TABLE IF NOT EXISTS incidents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			target TEXT NOT NULL,
			started_at INTEGER NOT NULL,
			resolved_at INTEGER,
			last_error TEXT,
			down_email_sent INTEGER DEFAULT 0,
			recovered_email_sent INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_incidents_open ON incidents(target, resolved_at)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w (%s)", err, q)
		}
	}
	return nil
}

func (s *Store) SaveCheck(c Check) error {
	_, err := s.db.Exec(
		`INSERT INTO checks(target, checked_at, status, latency_ms, error, detail) VALUES (?,?,?,?,?,?)`,
		c.Target, c.CheckedAt.Unix(), string(c.Status), c.LatencyMs, c.Error, c.Detail,
	)
	return err
}

func (s *Store) GetOpenIncident(target string) (*Incident, error) {
	row := s.db.QueryRow(
		`SELECT id, target, started_at, resolved_at, last_error, down_email_sent, recovered_email_sent
		 FROM incidents WHERE target = ? AND resolved_at IS NULL LIMIT 1`, target)
	return scanIncident(row)
}

func (s *Store) OpenIncident(target string, startedAt time.Time, lastError string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO incidents(target, started_at, last_error) VALUES (?,?,?)`,
		target, startedAt.Unix(), lastError)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateIncidentError(id int64, lastError string) error {
	_, err := s.db.Exec(`UPDATE incidents SET last_error=? WHERE id=?`, lastError, id)
	return err
}

func (s *Store) ResolveIncident(id int64, resolvedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE incidents SET resolved_at=? WHERE id=?`, resolvedAt.Unix(), id)
	return err
}

func (s *Store) MarkDownEmailSent(id int64) error {
	_, err := s.db.Exec(`UPDATE incidents SET down_email_sent=1 WHERE id=?`, id)
	return err
}

func (s *Store) MarkRecoveredEmailSent(id int64) error {
	_, err := s.db.Exec(`UPDATE incidents SET recovered_email_sent=1 WHERE id=?`, id)
	return err
}

func (s *Store) LatestCheck(target string) (*Check, error) {
	row := s.db.QueryRow(
		`SELECT id, target, checked_at, status, latency_ms, error, detail
		 FROM checks WHERE target=? ORDER BY checked_at DESC LIMIT 1`, target)
	return scanCheck(row)
}

func (s *Store) ListChecks(target string, since time.Time) ([]Check, error) {
	rows, err := s.db.Query(
		`SELECT id, target, checked_at, status, latency_ms, error, detail
		 FROM checks WHERE target=? AND checked_at >= ? ORDER BY checked_at ASC`,
		target, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Check
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) UptimePercent(target string, since time.Time) (float64, int, error) {
	row := s.db.QueryRow(
		`SELECT
			SUM(CASE WHEN status='ok' THEN 1 ELSE 0 END),
			COUNT(*)
		 FROM checks WHERE target=? AND checked_at >= ?`, target, since.Unix())
	var okCount, total sql.NullInt64
	if err := row.Scan(&okCount, &total); err != nil {
		return 0, 0, err
	}
	if !total.Valid || total.Int64 == 0 {
		return 0, 0, nil
	}
	return float64(okCount.Int64) / float64(total.Int64) * 100.0, int(total.Int64), nil
}

type DayStat struct {
	Date  time.Time
	Total int
	Down  int
}

// DailyRollup returns per-day OK/DOWN counts in the caller's local timezone,
// covering the range [since, until). Empty days are returned with Total=0.
func (s *Store) DailyRollup(target string, since, until time.Time) ([]DayStat, error) {
	startDay := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location())
	endDay := time.Date(until.Year(), until.Month(), until.Day(), 0, 0, 0, 0, until.Location())
	days := int(endDay.Sub(startDay).Hours()/24) + 1
	if days < 1 {
		days = 1
	}
	result := make([]DayStat, days)
	for i := range result {
		result[i] = DayStat{Date: startDay.Add(time.Duration(i) * 24 * time.Hour)}
	}

	rows, err := s.db.Query(
		`SELECT checked_at, status FROM checks WHERE target=? AND checked_at >= ? AND checked_at < ?`,
		target, startDay.Unix(), endDay.Add(24*time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ts int64
		var status string
		if err := rows.Scan(&ts, &status); err != nil {
			return nil, err
		}
		t := time.Unix(ts, 0).In(since.Location())
		idx := int(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).Sub(startDay).Hours() / 24)
		if idx < 0 || idx >= days {
			continue
		}
		result[idx].Total++
		if status != string(StatusOK) {
			result[idx].Down++
		}
	}
	return result, rows.Err()
}

func (s *Store) OpenIncidents() ([]Incident, error) {
	return s.queryIncidents(
		`SELECT id, target, started_at, resolved_at, last_error, down_email_sent, recovered_email_sent
		 FROM incidents WHERE resolved_at IS NULL ORDER BY started_at DESC`)
}

func (s *Store) RecentIncidents(since time.Time) ([]Incident, error) {
	return s.queryIncidents(
		`SELECT id, target, started_at, resolved_at, last_error, down_email_sent, recovered_email_sent
		 FROM incidents WHERE started_at >= ? ORDER BY started_at DESC`, since.Unix())
}

func (s *Store) TargetIncidents(target string, since time.Time) ([]Incident, error) {
	return s.queryIncidents(
		`SELECT id, target, started_at, resolved_at, last_error, down_email_sent, recovered_email_sent
		 FROM incidents WHERE target=? AND started_at >= ? ORDER BY started_at DESC`,
		target, since.Unix())
}

func (s *Store) PendingDownEmails(threshold time.Time) ([]Incident, error) {
	return s.queryIncidents(
		`SELECT id, target, started_at, resolved_at, last_error, down_email_sent, recovered_email_sent
		 FROM incidents WHERE resolved_at IS NULL AND down_email_sent=0 AND started_at <= ?`,
		threshold.Unix())
}

func (s *Store) Cleanup(retention time.Duration) (int64, int64, error) {
	cutoff := time.Now().Add(-retention).Unix()
	r1, err := s.db.Exec(`DELETE FROM checks WHERE checked_at < ?`, cutoff)
	if err != nil {
		return 0, 0, err
	}
	r2, err := s.db.Exec(`DELETE FROM incidents WHERE resolved_at IS NOT NULL AND resolved_at < ?`, cutoff)
	if err != nil {
		return 0, 0, err
	}
	deletedChecks, _ := r1.RowsAffected()
	deletedInc, _ := r2.RowsAffected()
	return deletedChecks, deletedInc, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCheck(r rowScanner) (*Check, error) {
	var c Check
	var ts int64
	var status string
	var errText, detail sql.NullString
	var latency sql.NullInt64
	if err := r.Scan(&c.ID, &c.Target, &ts, &status, &latency, &errText, &detail); err != nil {
		return nil, err
	}
	c.CheckedAt = time.Unix(ts, 0)
	c.Status = Status(status)
	c.LatencyMs = latency.Int64
	c.Error = errText.String
	c.Detail = detail.String
	return &c, nil
}

func scanIncident(r rowScanner) (*Incident, error) {
	var i Incident
	var started int64
	var resolved sql.NullInt64
	var lastErr sql.NullString
	var downSent, recoveredSent int
	if err := r.Scan(&i.ID, &i.Target, &started, &resolved, &lastErr, &downSent, &recoveredSent); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	i.StartedAt = time.Unix(started, 0)
	if resolved.Valid {
		t := time.Unix(resolved.Int64, 0)
		i.ResolvedAt = &t
	}
	i.LastError = lastErr.String
	i.DownEmailSent = downSent == 1
	i.RecoveredEmailSent = recoveredSent == 1
	return &i, nil
}

func (s *Store) queryIncidents(q string, args ...any) ([]Incident, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		if inc != nil {
			out = append(out, *inc)
		}
	}
	return out, rows.Err()
}
