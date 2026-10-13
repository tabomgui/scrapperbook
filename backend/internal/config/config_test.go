package config_test

import (
	"testing"
	"time"

	"scrapperbook/backend/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.Limits.MaxPerHour != 6 || c.Limits.MinGap != 4*time.Minute ||
		c.Limits.Lease != 10*time.Minute {
		t.Fatalf("config = %+v", c)
	}
	q := c.Limits.Quiet
	if !q.Enabled || q.StartHour != 23 || q.EndHour != 7 || q.Location.String() != "America/Sao_Paulo" {
		t.Fatalf("quiet = %+v", q)
	}
}

func TestQuietHoursOffAndOverrides(t *testing.T) {
	c, err := config.Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t",
		"QUIET_HOURS": "off", "FB_MAX_JOBS_PER_HOUR": "12", "FB_MIN_GAP": "90s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.Quiet.Enabled || c.Limits.MaxPerHour != 12 || c.Limits.MinGap != 90*time.Second {
		t.Fatalf("config = %+v", c)
	}
}

func TestInvalidConfig(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t"}
	cases := map[string]map[string]string{
		"missing db":     {"WORKER_TOKEN": "t"},
		"missing token":  {"DATABASE_URL": "postgres://x"},
		"bad quiet":      {"QUIET_HOURS": "25-7"},
		"bad quiet form": {"QUIET_HOURS": "night"},
		"bad gap":        {"FB_MIN_GAP": "soon"},
		"bad max":        {"FB_MAX_JOBS_PER_HOUR": "many"},
		"zero max":       {"FB_MAX_JOBS_PER_HOUR": "0"},
		"negative max":   {"FB_MAX_JOBS_PER_HOUR": "-1"},
		"negative gap":   {"FB_MIN_GAP": "-1m"},
		"zero job lease": {"JOB_LEASE": "0s"},
	}
	for name, override := range cases {
		m := map[string]string{}
		if name != "missing db" && name != "missing token" {
			for k, v := range base {
				m[k] = v
			}
		}
		for k, v := range override {
			m[k] = v
		}
		if _, err := config.Load(env(m)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestDiscordAndSweepDefaults(t *testing.T) {
	c, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DiscordWebhookURL != "" {
		t.Fatalf("discord webhook default = %q, want empty (disabled)", c.DiscordWebhookURL)
	}
	if c.ListingUnseenAfter != 7*24*time.Hour {
		t.Fatalf("listing unseen after = %s, want 168h", c.ListingUnseenAfter)
	}
	if c.RawCapturesRetention != 14*24*time.Hour {
		t.Fatalf("raw captures retention = %s, want 336h", c.RawCapturesRetention)
	}
}

func TestDiscordAndSweepOverridesAndValidation(t *testing.T) {
	c, err := config.Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t",
		"DISCORD_WEBHOOK_URL": "https://discord.com/api/webhooks/1/abc",
		"LISTING_UNSEEN_AFTER": "48h", "RAW_CAPTURES_RETENTION": "72h",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DiscordWebhookURL != "https://discord.com/api/webhooks/1/abc" {
		t.Fatalf("discord webhook = %q", c.DiscordWebhookURL)
	}
	if c.ListingUnseenAfter != 48*time.Hour || c.RawCapturesRetention != 72*time.Hour {
		t.Fatalf("unseen=%s retention=%s", c.ListingUnseenAfter, c.RawCapturesRetention)
	}

	for _, bad := range []map[string]string{
		{"LISTING_UNSEEN_AFTER": "soon"},
		{"LISTING_UNSEEN_AFTER": "0h"},
		{"RAW_CAPTURES_RETENTION": "-1h"},
	} {
		m := map[string]string{"DATABASE_URL": "postgres://x", "WORKER_TOKEN": "t"}
		for k, v := range bad {
			m[k] = v
		}
		if _, err := config.Load(env(m)); err == nil {
			t.Errorf("%v: expected error", bad)
		}
	}
}
