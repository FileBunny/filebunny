// Package config reads the FileBunny configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Config holds all runtime settings. Every field has a default, so an empty
// environment yields a working configuration.
type Config struct {
	// Listen is the TCP address the HTTP server listens on.
	Listen string
	// DataDir holds the SQLite database and the ciphertext blobs.
	DataDir string
	// MaxSize is the maximum upload size in bytes (ciphertext, including the
	// OpenPGP overhead).
	MaxSize int64
	// DefaultExpiry applies when an upload does not ask for an expiry.
	DefaultExpiry time.Duration
	// MaxExpiry is the longest expiry an upload may ask for.
	MaxExpiry time.Duration
	// CleanupInterval is how often expired files are removed.
	CleanupInterval time.Duration
}

// Defaults for the public service, see Besluiten (fase 0).
const (
	DefaultListen          = ":8080"
	DefaultDataDir         = "./data"
	DefaultMaxSize         = 100 << 20 // 100 MiB
	DefaultDefaultExpiry   = 24 * time.Hour
	DefaultMaxExpiry       = 7 * 24 * time.Hour
	DefaultCleanupInterval = time.Minute
)

// FromEnv builds a Config from the given lookup function (normally os.Getenv).
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		Listen:          DefaultListen,
		DataDir:         DefaultDataDir,
		MaxSize:         DefaultMaxSize,
		DefaultExpiry:   DefaultDefaultExpiry,
		MaxExpiry:       DefaultMaxExpiry,
		CleanupInterval: DefaultCleanupInterval,
	}
	var errs []error

	if v := getenv("FILEBUNNY_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := getenv("FILEBUNNY_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := getenv("FILEBUNNY_MAX_SIZE"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("FILEBUNNY_MAX_SIZE: want a positive number of bytes, got %q", v))
		} else {
			c.MaxSize = n
		}
	}
	durations := []struct {
		name string
		dst  *time.Duration
	}{
		{"FILEBUNNY_DEFAULT_EXPIRY", &c.DefaultExpiry},
		{"FILEBUNNY_MAX_EXPIRY", &c.MaxExpiry},
		{"FILEBUNNY_CLEANUP_INTERVAL", &c.CleanupInterval},
	}
	for _, d := range durations {
		v := getenv(d.name)
		if v == "" {
			continue
		}
		dur, err := time.ParseDuration(v)
		if err != nil || dur <= 0 {
			errs = append(errs, fmt.Errorf("%s: want a positive duration such as 24h, got %q", d.name, v))
			continue
		}
		*d.dst = dur
	}

	if c.DefaultExpiry > c.MaxExpiry {
		errs = append(errs, fmt.Errorf("default expiry %s exceeds max expiry %s", c.DefaultExpiry, c.MaxExpiry))
	}
	return c, errors.Join(errs...)
}
