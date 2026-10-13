// Package config reads server settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"scrapperbook/backend/internal/jobs"
)

type Config struct {
	DatabaseURL string
	WorkerToken string
	HTTPAddr    string
	Limits      jobs.Limits

	DiscordWebhookURL    string // empty disables Discord notifications, not an error
	ListingUnseenAfter   time.Duration
	RawCapturesRetention time.Duration
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{
		DatabaseURL: getenv("DATABASE_URL"),
		WorkerToken: getenv("WORKER_TOKEN"),
		HTTPAddr:    or(getenv("HTTP_ADDR"), ":8080"),
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if c.WorkerToken == "" {
		return Config{}, errors.New("WORKER_TOKEN is required")
	}
	loc, err := time.LoadLocation(or(getenv("TIMEZONE"), "America/Sao_Paulo"))
	if err != nil {
		return Config{}, fmt.Errorf("TIMEZONE: %w", err)
	}
	maxPerHour, err := strconv.Atoi(or(getenv("FB_MAX_JOBS_PER_HOUR"), "6"))
	if err != nil {
		return Config{}, fmt.Errorf("FB_MAX_JOBS_PER_HOUR: %w", err)
	}
	if maxPerHour <= 0 {
		// 0 wouldn't error anywhere downstream, it would just silently stop
		// the scheduler from ever claiming a job for this source.
		return Config{}, fmt.Errorf("FB_MAX_JOBS_PER_HOUR: must be positive, got %d", maxPerHour)
	}
	minGap, err := time.ParseDuration(or(getenv("FB_MIN_GAP"), "4m"))
	if err != nil {
		return Config{}, fmt.Errorf("FB_MIN_GAP: %w", err)
	}
	if minGap < 0 {
		return Config{}, fmt.Errorf("FB_MIN_GAP: must not be negative, got %s", minGap)
	}
	lease, err := time.ParseDuration(or(getenv("JOB_LEASE"), "10m"))
	if err != nil {
		return Config{}, fmt.Errorf("JOB_LEASE: %w", err)
	}
	if lease <= 0 {
		return Config{}, fmt.Errorf("JOB_LEASE: must be positive, got %s", lease)
	}
	quiet, err := parseQuietHours(or(getenv("QUIET_HOURS"), "23-7"), loc)
	if err != nil {
		return Config{}, err
	}
	c.Limits = jobs.Limits{MaxPerHour: maxPerHour, MinGap: minGap, Lease: lease, Quiet: quiet}

	listingUnseenAfter, err := time.ParseDuration(or(getenv("LISTING_UNSEEN_AFTER"), "168h"))
	if err != nil {
		return Config{}, fmt.Errorf("LISTING_UNSEEN_AFTER: %w", err)
	}
	if listingUnseenAfter <= 0 {
		return Config{}, fmt.Errorf("LISTING_UNSEEN_AFTER: must be positive, got %s", listingUnseenAfter)
	}
	rawCapturesRetention, err := time.ParseDuration(or(getenv("RAW_CAPTURES_RETENTION"), "336h"))
	if err != nil {
		return Config{}, fmt.Errorf("RAW_CAPTURES_RETENTION: %w", err)
	}
	if rawCapturesRetention <= 0 {
		return Config{}, fmt.Errorf("RAW_CAPTURES_RETENTION: must be positive, got %s", rawCapturesRetention)
	}
	c.DiscordWebhookURL = getenv("DISCORD_WEBHOOK_URL") // no default: empty means disabled
	c.ListingUnseenAfter = listingUnseenAfter
	c.RawCapturesRetention = rawCapturesRetention

	return c, nil
}

// parseQuietHours accepts "off" or "START-END" in whole hours, e.g. "23-7".
func parseQuietHours(s string, loc *time.Location) (jobs.QuietHours, error) {
	if s == "off" {
		return jobs.QuietHours{Location: loc}, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return jobs.QuietHours{}, fmt.Errorf("QUIET_HOURS %q: want off or START-END", s)
	}
	start, err1 := strconv.Atoi(a)
	end, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || start < 0 || start > 23 || end < 0 || end > 23 {
		return jobs.QuietHours{}, fmt.Errorf("QUIET_HOURS %q: hours must be 0-23", s)
	}
	return jobs.QuietHours{Enabled: true, StartHour: start, EndHour: end, Location: loc}, nil
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
