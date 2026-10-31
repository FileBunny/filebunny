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
	// MaxTotalSize caps the combined size of all stored files. Uploads that
	// could exceed it are refused, so anonymous uploads cannot fill the disk.
	MaxTotalSize int64
	// DefaultExpiry applies when an upload does not ask for an expiry.
	DefaultExpiry time.Duration
	// MaxExpiry is the longest expiry an upload may ask for.
	MaxExpiry time.Duration
	// CleanupInterval is how often expired files are removed.
	CleanupInterval time.Duration
	// MaxDownloads is the highest download limit an upload may ask for.
	// Uploads without a limit can be downloaded until they expire.
	MaxDownloads int
	// MaxPendingUploads caps the chunked uploads in progress, server-wide.
	MaxPendingUploads int
	// WKDProxy enables POST /api/v1/wkd, which fetches a key from the
	// recipient's domain (Web Key Directory) for the browser.
	WKDProxy bool
	// MaxInboxes caps the upload links (inboxes) that accept uploads,
	// server-wide.
	MaxInboxes int
	// InboxDefaultExpiry applies when a new upload link asks for none;
	// InboxMaxExpiry is the longest it may ask for.
	InboxDefaultExpiry time.Duration
	InboxMaxExpiry     time.Duration
	// InboxMaxUploads is the highest number of uploads a link may allow.
	InboxMaxUploads int
	// InboxMaxSize caps the combined size of the uploads to one link.
	InboxMaxSize int64
	// Filebin offers filebin.net as external storage for share links.
	// Off by default: a self-hoster decides whether to use a third party.
	Filebin bool
	// ProxyDailyBytes caps the bytes sent to and fetched from external
	// storage per day (UTC), server-wide: the server carries that traffic
	// twice, and the VPS has a traffic budget.
	ProxyDailyBytes int64
}

// Defaults for the public service at filebunny.eu.
const (
	DefaultListen             = ":8080"
	DefaultDataDir            = "./data"
	DefaultMaxSize            = 100 << 20 // 100 MiB
	DefaultProxyDailyBytes    = 50 << 30  // 50 GiB
	DefaultMaxTotalSize       = 20 << 30  // 20 GiB
	DefaultDefaultExpiry      = 24 * time.Hour
	DefaultMaxExpiry          = 7 * 24 * time.Hour
	DefaultCleanupInterval    = time.Minute
	DefaultMaxDownloads       = 100
	DefaultMaxPendingUploads  = 50
	DefaultMaxInboxes         = 1000
	DefaultInboxDefaultExpiry = 7 * 24 * time.Hour
	DefaultInboxMaxExpiry     = 30 * 24 * time.Hour
	DefaultInboxMaxUploads    = 100
	DefaultInboxMaxSize       = 100 << 20 // 100 MiB
)

// MinExpiry is the shortest expiry an upload may ask for.
const MinExpiry = time.Minute

// FromEnv builds a Config from the given lookup function (normally os.Getenv).
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		Listen:            DefaultListen,
		DataDir:           DefaultDataDir,
		MaxSize:           DefaultMaxSize,
		MaxTotalSize:      DefaultMaxTotalSize,
		DefaultExpiry:     DefaultDefaultExpiry,
		MaxExpiry:         DefaultMaxExpiry,
		CleanupInterval:   DefaultCleanupInterval,
		MaxDownloads:      DefaultMaxDownloads,
		MaxPendingUploads: DefaultMaxPendingUploads,
		WKDProxy:          true,

		MaxInboxes:         DefaultMaxInboxes,
		InboxDefaultExpiry: DefaultInboxDefaultExpiry,
		InboxMaxExpiry:     DefaultInboxMaxExpiry,
		InboxMaxUploads:    DefaultInboxMaxUploads,
		InboxMaxSize:       DefaultInboxMaxSize,
		ProxyDailyBytes:    DefaultProxyDailyBytes,
	}
	var errs []error

	if v := getenv("FILEBUNNY_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := getenv("FILEBUNNY_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	sizes := []struct {
		name string
		dst  *int64
	}{
		{"FILEBUNNY_MAX_SIZE", &c.MaxSize},
		{"FILEBUNNY_MAX_TOTAL_SIZE", &c.MaxTotalSize},
		{"FILEBUNNY_INBOX_MAX_SIZE", &c.InboxMaxSize},
		{"FILEBUNNY_PROXY_DAILY_BYTES", &c.ProxyDailyBytes},
	}
	for _, sz := range sizes {
		v := getenv(sz.name)
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("%s: want a positive number of bytes, got %q", sz.name, v))
			continue
		}
		*sz.dst = n
	}
	durations := []struct {
		name string
		dst  *time.Duration
	}{
		{"FILEBUNNY_DEFAULT_EXPIRY", &c.DefaultExpiry},
		{"FILEBUNNY_MAX_EXPIRY", &c.MaxExpiry},
		{"FILEBUNNY_CLEANUP_INTERVAL", &c.CleanupInterval},
		{"FILEBUNNY_INBOX_DEFAULT_EXPIRY", &c.InboxDefaultExpiry},
		{"FILEBUNNY_INBOX_MAX_EXPIRY", &c.InboxMaxExpiry},
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

	counts := []struct {
		name string
		dst  *int
	}{
		{"FILEBUNNY_MAX_DOWNLOADS", &c.MaxDownloads},
		{"FILEBUNNY_MAX_PENDING_UPLOADS", &c.MaxPendingUploads},
		{"FILEBUNNY_MAX_INBOXES", &c.MaxInboxes},
		{"FILEBUNNY_INBOX_MAX_UPLOADS", &c.InboxMaxUploads},
	}
	for _, n := range counts {
		v := getenv(n.name)
		if v == "" {
			continue
		}
		i, err := strconv.Atoi(v)
		if err != nil || i <= 0 {
			errs = append(errs, fmt.Errorf("%s: want a positive number, got %q", n.name, v))
			continue
		}
		*n.dst = i
	}

	bools := []struct {
		name string
		dst  *bool
	}{
		{"FILEBUNNY_WKD_PROXY", &c.WKDProxy},
		{"FILEBUNNY_FILEBIN", &c.Filebin},
	}
	for _, b := range bools {
		v := getenv(b.name)
		if v == "" {
			continue
		}
		val, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: want true or false, got %q", b.name, v))
			continue
		}
		*b.dst = val
	}

	if c.DefaultExpiry < MinExpiry {
		errs = append(errs, fmt.Errorf("default expiry %s is below the minimum %s", c.DefaultExpiry, MinExpiry))
	}
	if c.DefaultExpiry > c.MaxExpiry {
		errs = append(errs, fmt.Errorf("default expiry %s exceeds max expiry %s", c.DefaultExpiry, c.MaxExpiry))
	}
	if c.InboxDefaultExpiry < MinExpiry || c.InboxDefaultExpiry > c.InboxMaxExpiry {
		errs = append(errs, fmt.Errorf("upload link default expiry %s must be between %s and the max %s", c.InboxDefaultExpiry, MinExpiry, c.InboxMaxExpiry))
	}
	if c.InboxMaxSize > c.MaxTotalSize {
		errs = append(errs, fmt.Errorf("upload link max size %d exceeds max total size %d", c.InboxMaxSize, c.MaxTotalSize))
	}
	if c.MaxSize > c.MaxTotalSize {
		errs = append(errs, fmt.Errorf("max size %d exceeds max total size %d", c.MaxSize, c.MaxTotalSize))
	}
	return c, errors.Join(errs...)
}
