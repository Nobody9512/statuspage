package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Scheduler SchedulerConfig `yaml:"scheduler"`
	Storage   StorageConfig   `yaml:"storage"`
	Notifier  NotifierConfig  `yaml:"notifier"`
	Targets   []Target        `yaml:"targets"`
}

type ServerConfig struct {
	Listen             string    `yaml:"listen"`
	AutoRefreshSeconds int       `yaml:"auto_refresh_seconds"`
	BrandName          string    `yaml:"brand_name"`
	BasicAuth          BasicAuth `yaml:"basic_auth"`
}

type BasicAuth struct {
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

type SchedulerConfig struct {
	IntervalMinutes     int `yaml:"interval_minutes"`
	RetentionDays       int `yaml:"retention_days"`
	CheckTimeoutSeconds int `yaml:"check_timeout_seconds"`
}

type StorageConfig struct {
	SQLitePath string `yaml:"sqlite_path"`
}

type NotifierConfig struct {
	Enabled              bool   `yaml:"enabled"`
	DownThresholdMinutes int    `yaml:"down_threshold_minutes"`
	SMTP                 SMTP   `yaml:"smtp"`
}

type SMTP struct {
	Host     string   `yaml:"host"`
	Port     int      `yaml:"port"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
	From     string   `yaml:"from"`
	To       []string `yaml:"to"`
}

type Target struct {
	Name           string            `yaml:"name"`
	Type           string            `yaml:"type"`
	URL            string            `yaml:"url,omitempty"`
	Method         string            `yaml:"method,omitempty"`
	Headers        map[string]string `yaml:"headers,omitempty"`
	Body           string            `yaml:"body,omitempty"`
	ExpectStatus   []int             `yaml:"expect_status,omitempty"`
	TimeoutSeconds int               `yaml:"timeout_seconds,omitempty"`

	DSN  string `yaml:"dsn,omitempty"`
	Addr string `yaml:"addr,omitempty"`

	Password string `yaml:"password,omitempty"`
	DB       int    `yaml:"db,omitempty"`

	SOAPAction string `yaml:"soap_action,omitempty"`
	SOAPEnvelope string `yaml:"soap_envelope,omitempty"`

	FailThreshold        int `yaml:"fail_threshold,omitempty"`
	StaleReservedMinutes int `yaml:"stale_reserved_minutes,omitempty"`
}

func (t Target) Timeout(fallback time.Duration) time.Duration {
	if t.TimeoutSeconds > 0 {
		return time.Duration(t.TimeoutSeconds) * time.Second
	}
	return fallback
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	c.applyDefaults()
	return &c, nil
}

func (c *Config) validate() error {
	if c.Server.Listen == "" {
		return errors.New("server.listen is required")
	}
	if c.Storage.SQLitePath == "" {
		return errors.New("storage.sqlite_path is required")
	}
	if len(c.Targets) == 0 {
		return errors.New("at least one target is required")
	}
	seen := map[string]bool{}
	for i, t := range c.Targets {
		if t.Name == "" {
			return fmt.Errorf("targets[%d].name is required", i)
		}
		if seen[t.Name] {
			return fmt.Errorf("duplicate target name: %q", t.Name)
		}
		seen[t.Name] = true
		if t.Type == "" {
			return fmt.Errorf("targets[%d].type is required (%s)", i, t.Name)
		}
		switch t.Type {
		case "http", "soap":
			if t.URL == "" {
				return fmt.Errorf("targets[%d].url required for type %s", i, t.Type)
			}
		case "postgres", "laravel_jobs":
			if t.DSN == "" {
				return fmt.Errorf("targets[%d].dsn required for type %s", i, t.Type)
			}
		case "redis", "tcp":
			if t.Addr == "" {
				return fmt.Errorf("targets[%d].addr required for type %s", i, t.Type)
			}
		default:
			return fmt.Errorf("targets[%d] unknown type: %s", i, t.Type)
		}
	}
	if c.Notifier.Enabled {
		if c.Notifier.SMTP.Host == "" || c.Notifier.SMTP.From == "" || len(c.Notifier.SMTP.To) == 0 {
			return errors.New("notifier.smtp host/from/to required when enabled")
		}
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Scheduler.IntervalMinutes == 0 {
		c.Scheduler.IntervalMinutes = 5
	}
	if c.Scheduler.RetentionDays == 0 {
		c.Scheduler.RetentionDays = 30
	}
	if c.Scheduler.CheckTimeoutSeconds == 0 {
		c.Scheduler.CheckTimeoutSeconds = 10
	}
	if c.Server.AutoRefreshSeconds == 0 {
		c.Server.AutoRefreshSeconds = 30
	}
	if c.Notifier.DownThresholdMinutes == 0 {
		c.Notifier.DownThresholdMinutes = 15
	}
	if c.Server.BrandName == "" {
		c.Server.BrandName = "Statuspage"
	}
}
