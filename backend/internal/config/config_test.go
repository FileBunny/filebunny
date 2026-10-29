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
	if c.MaxDownloads != 100 || c.MaxPendingUploads != 50 || !c.WKDProxy {
		t.Errorf("unexpected limits: %+v", c)
	}
	if c.MaxSize != 100<<20 || c.DefaultExpiry != 24*time.Hour || c.MaxExpiry != 7*24*time.Hour {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestOverrides(t *testing.T) {
	c, err := FromEnv(env(map[string]string{
		"FILEBUNNY_LISTEN":              "127.0.0.1:9000",
		"FILEBUNNY_DATA_DIR":            "/srv/fb",
		"FILEBUNNY_MAX_SIZE":            "1024",
		"FILEBUNNY_MAX_TOTAL_SIZE":      "4096",
		"FILEBUNNY_DEFAULT_EXPIRY":      "1h",
		"FILEBUNNY_MAX_EXPIRY":          "2h",
		"FILEBUNNY_MAX_DOWNLOADS":       "5",
		"FILEBUNNY_MAX_PENDING_UPLOADS": "6",
		"FILEBUNNY_WKD_PROXY":           "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || c.DataDir != "/srv/fb" || c.MaxSize != 1024 || c.MaxTotalSize != 4096 ||
		c.DefaultExpiry != time.Hour || c.MaxExpiry != 2*time.Hour || c.MaxDownloads != 5 || c.MaxPendingUploads != 6 || c.WKDProxy {
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
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := FromEnv(env(m)); err == nil {
				t.Error("expected error")
			}
		})
	}
}
