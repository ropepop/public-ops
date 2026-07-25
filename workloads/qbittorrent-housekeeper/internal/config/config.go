package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultSoftCapBytes int64 = 24 * 1024 * 1024 * 1024

type Config struct {
	QBitURL        string
	Username       string
	PasswordFile   string
	DownloadPath   string
	SoftCapBytes   int64
	MinAge         time.Duration
	MinRatio       float64
	PollInterval   time.Duration
	RequestTimeout time.Duration
	HealthAddr     string
	HealthMaxAge   time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		QBitURL:        envOr("QBITTORRENT_URL", "http://qbittorrent:8080"),
		Username:       strings.TrimSpace(os.Getenv("QBITTORRENT_USERNAME")),
		PasswordFile:   strings.TrimSpace(os.Getenv("QBITTORRENT_PASSWORD_FILE")),
		DownloadPath:   envOr("DOWNLOAD_PATH", "/downloads"),
		SoftCapBytes:   defaultSoftCapBytes,
		MinAge:         7 * 24 * time.Hour,
		MinRatio:       1,
		PollInterval:   30 * time.Second,
		RequestTimeout: 10 * time.Second,
		HealthAddr:     envOr("HEALTH_ADDR", ":9091"),
		HealthMaxAge:   2 * time.Minute,
	}

	var err error
	if value := strings.TrimSpace(os.Getenv("SOFT_CAP_BYTES")); value != "" {
		cfg.SoftCapBytes, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parse SOFT_CAP_BYTES: %w", err)
		}
	}
	if value := strings.TrimSpace(os.Getenv("MIN_COMPLETED_AGE")); value != "" {
		cfg.MinAge, err = time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse MIN_COMPLETED_AGE: %w", err)
		}
	}
	if value := strings.TrimSpace(os.Getenv("MIN_RATIO")); value != "" {
		cfg.MinRatio, err = strconv.ParseFloat(value, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parse MIN_RATIO: %w", err)
		}
	}
	if value := strings.TrimSpace(os.Getenv("POLL_INTERVAL")); value != "" {
		cfg.PollInterval, err = time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse POLL_INTERVAL: %w", err)
		}
	}
	if value := strings.TrimSpace(os.Getenv("REQUEST_TIMEOUT")); value != "" {
		cfg.RequestTimeout, err = time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse REQUEST_TIMEOUT: %w", err)
		}
	}
	if value := strings.TrimSpace(os.Getenv("HEALTH_MAX_AGE")); value != "" {
		cfg.HealthMaxAge, err = time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse HEALTH_MAX_AGE: %w", err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.QBitURL) == "" {
		return errors.New("QBITTORRENT_URL is required")
	}
	if strings.TrimSpace(c.DownloadPath) == "" {
		return errors.New("DOWNLOAD_PATH is required")
	}
	cleanDownloadPath := filepath.Clean(c.DownloadPath)
	if !filepath.IsAbs(cleanDownloadPath) || cleanDownloadPath == string(filepath.Separator) {
		return errors.New("DOWNLOAD_PATH must be an absolute dedicated directory, not the filesystem root")
	}
	if c.SoftCapBytes <= 0 {
		return errors.New("SOFT_CAP_BYTES must be positive")
	}
	if c.MinAge < 0 {
		return errors.New("MIN_COMPLETED_AGE must not be negative")
	}
	if c.MinRatio < 0 || math.IsNaN(c.MinRatio) || math.IsInf(c.MinRatio, 0) {
		return errors.New("MIN_RATIO must be a finite non-negative number")
	}
	if c.PollInterval <= 0 {
		return errors.New("POLL_INTERVAL must be positive")
	}
	if c.RequestTimeout <= 0 {
		return errors.New("REQUEST_TIMEOUT must be positive")
	}
	if strings.TrimSpace(c.HealthAddr) == "" {
		return errors.New("HEALTH_ADDR is required")
	}
	if c.HealthMaxAge <= 0 {
		return errors.New("HEALTH_MAX_AGE must be positive")
	}
	if (c.Username == "") != (c.PasswordFile == "") {
		return errors.New("QBITTORRENT_USERNAME and QBITTORRENT_PASSWORD_FILE must be configured together")
	}
	return nil
}

func (c Config) ReadPassword() (string, error) {
	if c.Username == "" {
		return "", nil
	}
	value, err := os.ReadFile(c.PasswordFile)
	if err != nil {
		return "", fmt.Errorf("read qBittorrent password file: %w", err)
	}
	password := strings.TrimRight(string(value), "\r\n")
	if password == "" {
		return "", errors.New("qBittorrent password file is empty")
	}
	return password, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
