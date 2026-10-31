package config

import (
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxDownloads != 100 || c.MaxPendingUploads != 50 || !c.WKDProxy || c.MaxInboxes != 1000 ||
		c.InboxDefaultExpiry != 7*24*time.Hour || c.InboxMaxExpiry != 30*24*time.Hour || c.InboxMaxUploads != 100 || c.InboxMaxSize != 100<<20 ||
		c.Filebin || c.ProxyDailyBytes != 50<<30 {
		t.Errorf("unexpected limits: %+v", c)
	}
	if c.MaxSize != 100<<20 || c.DefaultExpiry != 24*time.Hour || c.MaxExpiry != 7*24*time.Hour {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestOverrides(t *testing.T) {
	c, err := FromEnv(env(map[string]string{
		"FILEBUNNY_LISTEN":               "127.0.0.1:9000",
		"FILEBUNNY_DATA_DIR":             "/srv/fb",
		"FILEBUNNY_MAX_SIZE":             "1024",
		"FILEBUNNY_MAX_TOTAL_SIZE":       "4096",
		"FILEBUNNY_DEFAULT_EXPIRY":       "1h",
		"FILEBUNNY_MAX_EXPIRY":           "2h",
		"FILEBUNNY_MAX_DOWNLOADS":        "5",
		"FILEBUNNY_MAX_PENDING_UPLOADS":  "6",
		"FILEBUNNY_WKD_PROXY":            "false",
		"FILEBUNNY_MAX_INBOXES":          "7",
		"FILEBUNNY_INBOX_DEFAULT_EXPIRY": "1h",
		"FILEBUNNY_INBOX_MAX_EXPIRY":     "3h",
		"FILEBUNNY_INBOX_MAX_UPLOADS":    "8",
		"FILEBUNNY_INBOX_MAX_SIZE":       "2048",
		"FILEBUNNY_FILEBIN":              "true",
		"FILEBUNNY_PROXY_DAILY_BYTES":    "512",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || c.DataDir != "/srv/fb" || c.MaxSize != 1024 || c.MaxTotalSize != 4096 ||
		c.DefaultExpiry != time.Hour || c.MaxExpiry != 2*time.Hour || c.MaxDownloads != 5 || c.MaxPendingUploads != 6 || c.WKDProxy ||
		c.MaxInboxes != 7 || c.InboxDefaultExpiry != time.Hour || c.InboxMaxExpiry != 3*time.Hour || c.InboxMaxUploads != 8 || c.InboxMaxSize != 2048 ||
		!c.Filebin || c.ProxyDailyBytes != 512 {
		t.Errorf("overrides not applied: %+v", c)
	}
}

func TestInvalid(t *testing.T) {
	cases := map[string]map[string]string{
		"size not a number":   {"FILEBUNNY_MAX_SIZE": "100MB"},
		"size zero":           {"FILEBUNNY_MAX_SIZE": "0"},
		"bad duration":        {"FILEBUNNY_MAX_EXPIRY": "7d"},
		"negative duration":   {"FILEBUNNY_CLEANUP_INTERVAL": "-1m"},
		"default exceeds max": {"FILEBUNNY_DEFAULT_EXPIRY": "200h"},
		"size exceeds total":  {"FILEBUNNY_MAX_TOTAL_SIZE": "1000"},
		"default below min":   {"FILEBUNNY_DEFAULT_EXPIRY": "30s"},
		"downloads zero":      {"FILEBUNNY_MAX_DOWNLOADS": "0"},
		"pending not number":  {"FILEBUNNY_MAX_PENDING_UPLOADS": "many"},
		"wkd not a bool":      {"FILEBUNNY_WKD_PROXY": "maybe"},
		"filebin not a bool":  {"FILEBUNNY_FILEBIN": "yes please"},
		"proxy bytes zero":    {"FILEBUNNY_PROXY_DAILY_BYTES": "0"},
		"inbox expiry order":  {"FILEBUNNY_INBOX_DEFAULT_EXPIRY": "800h"},
		"inbox size > total":  {"FILEBUNNY_INBOX_MAX_SIZE": "1000", "FILEBUNNY_MAX_TOTAL_SIZE": "999", "FILEBUNNY_MAX_SIZE": "10"},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := FromEnv(env(m)); err == nil {
				t.Error("expected error")
			}
		})
	}
}
