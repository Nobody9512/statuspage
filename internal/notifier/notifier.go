package notifier

import (
	"context"
	"fmt"
	"log"
	"net/smtp"
	"strings"
	"time"

	"github.com/Nobody9512/statuspage/internal/config"
	"github.com/Nobody9512/statuspage/internal/storage"
)

type Notifier struct {
	cfg   config.NotifierConfig
	store *storage.Store
}

func New(cfg config.NotifierConfig, store *storage.Store) *Notifier {
	return &Notifier{cfg: cfg, store: store}
}

func (n *Notifier) Enabled() bool { return n.cfg.Enabled }

func (n *Notifier) ProcessPendingDown(ctx context.Context) {
	if !n.cfg.Enabled {
		return
	}
	threshold := time.Now().Add(-time.Duration(n.cfg.DownThresholdMinutes) * time.Minute)
	incidents, err := n.store.PendingDownEmails(threshold)
	if err != nil {
		log.Printf("notifier: pending query: %v", err)
		return
	}
	for _, inc := range incidents {
		subject := fmt.Sprintf("[ERP Monitor] DOWN: %s", inc.Target)
		duration := time.Since(inc.StartedAt).Round(time.Second)
		body := fmt.Sprintf(
			"Service %q has been DOWN for %s.\n\nStarted at: %s\nLast error: %s\n",
			inc.Target, duration, inc.StartedAt.Format(time.RFC3339), inc.LastError)
		if err := n.send(subject, body); err != nil {
			log.Printf("notifier: send DOWN email for %s: %v", inc.Target, err)
			continue
		}
		if err := n.store.MarkDownEmailSent(inc.ID); err != nil {
			log.Printf("notifier: mark down_email_sent: %v", err)
		}
	}
}

func (n *Notifier) NotifyRecovered(inc storage.Incident) {
	if !n.cfg.Enabled {
		return
	}
	if !inc.DownEmailSent || inc.RecoveredEmailSent {
		return
	}
	resolvedAt := time.Now()
	if inc.ResolvedAt != nil {
		resolvedAt = *inc.ResolvedAt
	}
	duration := resolvedAt.Sub(inc.StartedAt).Round(time.Second)
	subject := fmt.Sprintf("[ERP Monitor] RECOVERED: %s", inc.Target)
	body := fmt.Sprintf(
		"Service %q is back UP.\n\nDowntime: %s\nStarted:  %s\nResolved: %s\n",
		inc.Target, duration, inc.StartedAt.Format(time.RFC3339), resolvedAt.Format(time.RFC3339))
	if err := n.send(subject, body); err != nil {
		log.Printf("notifier: send RECOVERED email for %s: %v", inc.Target, err)
		return
	}
	if err := n.store.MarkRecoveredEmailSent(inc.ID); err != nil {
		log.Printf("notifier: mark recovered_email_sent: %v", err)
	}
}

func (n *Notifier) send(subject, body string) error {
	s := n.cfg.SMTP
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	msg := buildMessage(s.From, s.To, subject, body)
	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}
	return smtp.SendMail(addr, auth, s.From, s.To, msg)
}

func buildMessage(from string, to []string, subject, body string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}
