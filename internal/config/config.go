// Package config loads and validates the process configuration from
// environment variables. Every variable is prefixed with COUCHCAST_.
//
// The same Config type serves both the web server and the ingest worker;
// each command validates only the sections it actually uses.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tkconfig "github.com/anesthetised/toolkit/config"
	"github.com/anesthetised/toolkit/os/env"
)

const prefix = "COUCHCAST_"

// Config is the root configuration shared by all commands.
type Config struct {
	LogLevel string

	Database DatabaseConfig
	S3       S3Config
	Web      WebConfig
	Ingest   IngestConfig
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	URL string
}

// S3Config holds settings for the S3-compatible media store.
type S3Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// WebConfig holds settings used only by the web server.
type WebConfig struct {
	Addr              string
	SessionTTL        time.Duration
	SecureCookies     bool
	MediaTokenSecret  string
	MediaTokenTTL     time.Duration
	ChatRetentionDays int
	MaxCacheBytes     int64
	// ProbeConcurrency caps link previews and playlist lookups (yt-dlp
	// runs) in flight at once.
	ProbeConcurrency int
	// TrustProxy makes rate limiting use X-Forwarded-For. Enable only behind
	// a reverse proxy that overwrites the header.
	TrustProxy bool
	// AuthRatePerMinute caps register/login attempts per client IP.
	AuthRatePerMinute int
	// VAPID keys enable Web Push; empty disables it. Generate a pair
	// with `couchcast vapid`.
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string
}

// IngestConfig holds settings used only by the ingest worker.
type IngestConfig struct {
	Workers        int
	MetricsAddr    string
	WorkDir        string
	YTDLPPath      string
	YTDLPExtraArgs []string
	FFmpegPath     string
	QualityLadder  []int
	SegmentSeconds int
	// AllowPrivateSources lets links point at loopback, private and
	// link-local addresses (a NAS on the LAN). Off by default: anyone who
	// can queue a video could otherwise make the server fetch from its own
	// network. Read by the web server (admission) and the worker.
	AllowPrivateSources bool
}

// Load reads the configuration from the environment. It does not validate
// anything: commands call Validate on the sections they need.
func Load() Config {
	return tkconfig.New(fromEnv).Val()
}

func fromEnv(c *Config) {
	c.LogLevel = env.Get(prefix+"LOG_LEVEL", "info")

	c.Database.URL = env.Get(prefix+"DATABASE_URL", "")

	c.S3.Endpoint = env.Get(prefix+"S3_ENDPOINT", "")
	c.S3.Bucket = env.Get(prefix+"S3_BUCKET", "couchcast")
	c.S3.AccessKey = env.Get(prefix+"S3_ACCESS_KEY", "")
	c.S3.SecretKey = env.Get(prefix+"S3_SECRET_KEY", "")
	c.S3.UseSSL = env.Get(prefix+"S3_USE_SSL", false)

	c.Web.Addr = env.Get(prefix+"ADDR", ":8080")
	c.Web.SessionTTL = env.Get(prefix+"SESSION_TTL", 30*24*time.Hour)
	c.Web.SecureCookies = env.Get(prefix+"SECURE_COOKIES", true)
	c.Web.MediaTokenSecret = env.Get(prefix+"MEDIA_TOKEN_SECRET", "")
	c.Web.MediaTokenTTL = env.Get(prefix+"MEDIA_TOKEN_TTL", time.Hour)
	c.Web.ChatRetentionDays = env.Get(prefix+"CHAT_RETENTION_DAYS", 30)
	c.Web.MaxCacheBytes = env.Get(prefix+"MAX_CACHE_BYTES", int64(50<<30))
	c.Web.TrustProxy = env.Get(prefix+"TRUST_PROXY", false)
	c.Web.ProbeConcurrency = env.Get(prefix+"PROBE_CONCURRENCY", 4)
	c.Web.AuthRatePerMinute = env.Get(prefix+"AUTH_RATE_PER_MINUTE", 10)
	c.Web.VAPIDPublicKey = env.Get(prefix+"VAPID_PUBLIC_KEY", "")
	c.Web.VAPIDPrivateKey = env.Get(prefix+"VAPID_PRIVATE_KEY", "")
	c.Web.VAPIDSubject = env.Get(prefix+"VAPID_SUBJECT", "")

	c.Ingest.Workers = env.Get(prefix+"INGEST_WORKERS", 2)
	c.Ingest.MetricsAddr = env.Get(prefix+"INGEST_METRICS_ADDR", ":9090")
	c.Ingest.WorkDir = env.Get(prefix+"WORK_DIR", "/tmp/couchcast")
	c.Ingest.YTDLPPath = env.Get(prefix+"YTDLP_PATH", "yt-dlp")
	c.Ingest.YTDLPExtraArgs = strings.Fields(env.Get(prefix+"YTDLP_EXTRA_ARGS", ""))
	c.Ingest.FFmpegPath = env.Get(prefix+"FFMPEG_PATH", "ffmpeg")
	c.Ingest.QualityLadder = parseIntList(env.Get(prefix+"QUALITY_LADDER", "1080,720,480,360"))
	c.Ingest.SegmentSeconds = env.Get(prefix+"SEGMENT_SECONDS", 4)
	c.Ingest.AllowPrivateSources = env.Get(prefix+"ALLOW_PRIVATE_SOURCES", false)
}

// parseIntList parses a comma-separated list of integers, silently dropping
// entries that do not parse; validation reports an empty result later.
func parseIntList(s string) []int {
	var out []int
	for part := range strings.SplitSeq(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// Validate checks every section. Commands that use only part of the
// configuration should validate the individual sections instead.
func (c Config) Validate() error {
	return errors.Join(
		c.Database.Validate(),
		c.S3.Validate(),
		c.Web.Validate(),
		c.Ingest.Validate(),
	)
}

// Validate implements validate.Validator.
func (c DatabaseConfig) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("%sDATABASE_URL is required", prefix)
	}
	return nil
}

// Validate implements validate.Validator.
func (c S3Config) Validate() error {
	var errs []error
	if c.Endpoint == "" {
		errs = append(errs, fmt.Errorf("%sS3_ENDPOINT is required", prefix))
	}
	if c.Bucket == "" {
		errs = append(errs, fmt.Errorf("%sS3_BUCKET is required", prefix))
	}
	if c.AccessKey == "" || c.SecretKey == "" {
		errs = append(errs, fmt.Errorf("%sS3_ACCESS_KEY and %sS3_SECRET_KEY are required", prefix, prefix))
	}
	return errors.Join(errs...)
}

// Validate implements validate.Validator.
func (c WebConfig) Validate() error {
	var errs []error
	if c.Addr == "" {
		errs = append(errs, fmt.Errorf("%sADDR is required", prefix))
	}
	if c.SessionTTL <= 0 {
		errs = append(errs, fmt.Errorf("%sSESSION_TTL must be positive", prefix))
	}
	if len(c.MediaTokenSecret) < 32 {
		errs = append(errs, fmt.Errorf("%sMEDIA_TOKEN_SECRET must be at least 32 characters", prefix))
	}
	if c.MediaTokenTTL <= 0 {
		errs = append(errs, fmt.Errorf("%sMEDIA_TOKEN_TTL must be positive", prefix))
	}
	if c.ChatRetentionDays < 0 {
		errs = append(errs, fmt.Errorf("%sCHAT_RETENTION_DAYS must not be negative", prefix))
	}
	if c.AuthRatePerMinute < 1 {
		errs = append(errs, fmt.Errorf("%sAUTH_RATE_PER_MINUTE must be at least 1", prefix))
	}
	return errors.Join(errs...)
}

// Validate implements validate.Validator.
func (c IngestConfig) Validate() error {
	var errs []error
	if c.Workers < 1 {
		errs = append(errs, fmt.Errorf("%sINGEST_WORKERS must be at least 1", prefix))
	}
	if c.WorkDir == "" {
		errs = append(errs, fmt.Errorf("%sWORK_DIR is required", prefix))
	}
	if len(c.QualityLadder) == 0 {
		errs = append(errs, fmt.Errorf("%sQUALITY_LADDER must contain at least one height", prefix))
	}
	if c.SegmentSeconds < 1 {
		errs = append(errs, fmt.Errorf("%sSEGMENT_SECONDS must be at least 1", prefix))
	}
	return errors.Join(errs...)
}
