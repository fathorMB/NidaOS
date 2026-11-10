// Package config loads the configuration of the core service from
// environment variables. There is no configuration file. This is the same
// rule as GitStack core: a systemd unit or a container sets the variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
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

	// Root is the start of the paths of /proc, /sys and /etc. It is "/" on
	// NidaOS. A different value lets the service read a copy of these files
	// on a development computer.
	Root string

	// PlanFile is the disk plan of the installer, from Root.
	PlanFile string

	// PodLogDir is the directory of the pod logs of the kubelet, from Root.
	// The metrics read the names of the pods from it.
	PodLogDir string

	// MetricsInterval is the time between two metric snapshots.
	MetricsInterval time.Duration

	// Journalctl is the command that reads the systemd journal. It is
	// empty when the value of the variable is "off".
	Journalctl []string

	// LogBufferLines is the number of log lines in memory.
	LogBufferLines int
}

// Defaults of the configuration.
const (
	DefaultAddr            = "127.0.0.1:8470"
	DefaultShutdownTimeout = 10 * time.Second
	DefaultRoot            = "/"
	DefaultPlanFile        = "/var/lib/nidaos/install-plan.txt"
	DefaultPodLogDir       = "/var/log/pods"
	DefaultMetricsInterval = 2 * time.Second
	// MinMetricsInterval protects the computer from a sampler that uses
	// too much processor time.
	MinMetricsInterval    = 250 * time.Millisecond
	DefaultLogBufferLines = 5000
)

// DefaultJournalctl is the default command for the journal.
var DefaultJournalctl = []string{"journalctl"}

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
		Root:            DefaultRoot,
		PlanFile:        DefaultPlanFile,
		PodLogDir:       DefaultPodLogDir,
		MetricsInterval: DefaultMetricsInterval,
		Journalctl:      DefaultJournalctl,
		LogBufferLines:  DefaultLogBufferLines,
	}

	if v := strings.TrimSpace(getenv(Prefix + "ADDR")); v != "" {
		cfg.Addr = v
	}
	if v := strings.TrimSpace(getenv(Prefix + "ROOT")); v != "" {
		cfg.Root = v
	}
	if v := strings.TrimSpace(getenv(Prefix + "PLAN_FILE")); v != "" {
		cfg.PlanFile = v
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

	if v := strings.TrimSpace(getenv(Prefix + "POD_LOG_DIR")); v != "" {
		cfg.PodLogDir = v
	}

	if v := strings.TrimSpace(getenv(Prefix + "METRICS_INTERVAL")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < MinMetricsInterval {
			return Config{}, fmt.Errorf("%sMETRICS_INTERVAL: the value %q is not a duration of %s or more", Prefix, v, MinMetricsInterval)
		}
		cfg.MetricsInterval = d
	}

	if v := strings.TrimSpace(getenv(Prefix + "JOURNALCTL")); v != "" {
		if v == "off" {
			cfg.Journalctl = nil
		} else {
			cfg.Journalctl = strings.Fields(v)
		}
	}

	if v := strings.TrimSpace(getenv(Prefix + "LOG_BUFFER_LINES")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 100 || n > 1_000_000 {
			return Config{}, fmt.Errorf("%sLOG_BUFFER_LINES: the value %q is not a number from 100 to 1000000", Prefix, v)
		}
		cfg.LogBufferLines = n
	}

	return cfg, nil
}
