// Package config loads the configuration of the core service from
// environment variables. There is no configuration file. This is the same
// rule as GitStack core: a systemd unit or a container sets the variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Prefix is the prefix of each environment variable of the core service.
const Prefix = "NIDA_CORE_"

// Config is the configuration of the core service.
type Config struct {
	// Addr is the HTTP listen address, for example "127.0.0.1:8470".
	Addr string

	// LogLevel is the minimum level of the service log.
	LogLevel slog.Level

	// ShutdownTimeout is the maximum time for the open requests when the
	// service stops.
	ShutdownTimeout time.Duration
}

// Defaults of the configuration.
const (
	DefaultAddr            = "127.0.0.1:8470"
	DefaultShutdownTimeout = 10 * time.Second
)

// Load reads the configuration from the environment of the process.
func Load() (Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads the configuration with the getenv function. The tests use
// it to give a different environment.
func LoadFrom(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:            DefaultAddr,
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: DefaultShutdownTimeout,
	}

	if v := strings.TrimSpace(getenv(Prefix + "ADDR")); v != "" {
		cfg.Addr = v
	}

	if v := strings.TrimSpace(getenv(Prefix + "LOG_LEVEL")); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return Config{}, fmt.Errorf("%sLOG_LEVEL: the value %q is not a log level (debug, info, warn, error)", Prefix, v)
		}
	}

	if v := strings.TrimSpace(getenv(Prefix + "SHUTDOWN_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("%sSHUTDOWN_TIMEOUT: the value %q is not a positive duration (for example 10s)", Prefix, v)
		}
		cfg.ShutdownTimeout = d
	}

	return cfg, nil
}
