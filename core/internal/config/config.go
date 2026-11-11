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

	// LogDir is the directory of the log store (an absolute path of the
	// host, not from Root). It is empty when the value is "off": then the
	// service keeps the logs only in memory.
	LogDir string

	// LogRetention is the time that the store keeps the log lines.
	LogRetention time.Duration

	// LogMaxBytes is the maximum size of the log store. 0 is no limit.
	LogMaxBytes int64

	// AuthFile keeps the owner password, the sessions and the tokens.
	AuthFile string

	// Socket is the path of the local socket (decision D18). It is empty
	// when the value is "off".
	Socket string

	// SocketGroup is the group that can open the local socket.
	SocketGroup string

	// AppsFile keeps the apps (milestone M-03). It is empty when the value
	// is "off": then the service has no app hosting.
	AppsFile string

	// Kubeconfig is the access file of k3s for the app controller.
	Kubeconfig string

	// AppsDomain gives the address "<app>.<AppsDomain>" of each app
	// (decision D19).
	AppsDomain string

	// Registry is the local image registry (decision D21). A deploy accepts
	// only an image from it.
	Registry string

	// CADir keeps the certificate authority of NidaOS (decision D20).
	CADir string

	// MDNS is false when the value of NIDA_CORE_MDNS is "off": then the
	// service does not announce the app names on the home network.
	MDNS bool

	// DatabasesFile keeps the databases (milestone M-04). It is empty when
	// the value is "off": then the service has no database hosting.
	DatabasesFile string

	// BackupDir keeps the backups of the databases (decision D24).
	BackupDir string

	// DatabaseImages gives the image of each PostgreSQL major version
	// (decision D23).
	DatabaseImages map[int]string
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
	DefaultLogDir         = "/var/lib/nidaos/logs"
	DefaultLogRetention   = 30 * 24 * time.Hour
	DefaultLogMaxBytes    = 10 << 30 // 10 GiB
	DefaultAuthFile       = "/var/lib/nidaos/auth.json"
	DefaultSocket         = "/run/nidaos/core.sock"
	DefaultSocketGroup    = "nida"
	DefaultAppsFile       = "/var/lib/nidaos/apps.json"
	DefaultKubeconfig     = "/etc/rancher/k3s/k3s.yaml"
	DefaultAppsDomain     = "nidaos.local"
	DefaultRegistry       = "localhost:5000"
	DefaultCADir          = "/var/lib/nidaos/ca"
	DefaultDatabasesFile  = "/var/lib/nidaos/databases.json"
	DefaultBackupDir      = "/var/nida/standard/backups"
	// DefaultDatabaseImages are the PostgreSQL images (decision D23).
	DefaultDatabaseImages = "17=docker.io/library/postgres:17.7-alpine,18=docker.io/library/postgres:18.1-alpine"
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
		LogDir:          DefaultLogDir,
		LogRetention:    DefaultLogRetention,
		LogMaxBytes:     DefaultLogMaxBytes,
		AuthFile:        DefaultAuthFile,
		Socket:          DefaultSocket,
		SocketGroup:     DefaultSocketGroup,
		AppsFile:        DefaultAppsFile,
		Kubeconfig:      DefaultKubeconfig,
		AppsDomain:      DefaultAppsDomain,
		Registry:        DefaultRegistry,
		CADir:           DefaultCADir,
		MDNS:            true,
		DatabasesFile:   DefaultDatabasesFile,
		BackupDir:       DefaultBackupDir,
	}
	images := DefaultDatabaseImages
	if v := strings.TrimSpace(getenv(Prefix + "DB_IMAGES")); v != "" {
		images = v
	}
	var err error
	if cfg.DatabaseImages, err = parseImages(images); err != nil {
		return Config{}, fmt.Errorf("%sDB_IMAGES: %w", Prefix, err)
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

	if v := strings.TrimSpace(getenv(Prefix + "LOG_DIR")); v != "" {
		if v == "off" {
			cfg.LogDir = ""
		} else {
			cfg.LogDir = v
		}
	}

	if v := strings.TrimSpace(getenv(Prefix + "LOG_RETENTION")); v != "" {
		d, err := parseDays(v)
		if err != nil || d < time.Hour {
			return Config{}, fmt.Errorf("%sLOG_RETENTION: the value %q is not a duration of 1h or more (for example 30d or 720h)", Prefix, v)
		}
		cfg.LogRetention = d
	}

	if v := strings.TrimSpace(getenv(Prefix + "LOG_MAX_SIZE")); v != "" {
		n, err := parseSize(v)
		if err != nil {
			return Config{}, fmt.Errorf("%sLOG_MAX_SIZE: %w", Prefix, err)
		}
		cfg.LogMaxBytes = n
	}

	if v := strings.TrimSpace(getenv(Prefix + "AUTH_FILE")); v != "" {
		cfg.AuthFile = v
	}
	if v := strings.TrimSpace(getenv(Prefix + "SOCKET")); v != "" {
		if v == "off" {
			cfg.Socket = ""
		} else {
			cfg.Socket = v
		}
	}
	if v := strings.TrimSpace(getenv(Prefix + "SOCKET_GROUP")); v != "" {
		cfg.SocketGroup = v
	}

	if v := strings.TrimSpace(getenv(Prefix + "APPS_FILE")); v != "" {
		if v == "off" {
			cfg.AppsFile = ""
		} else {
			cfg.AppsFile = v
		}
	}
	if v := strings.TrimSpace(getenv(Prefix + "KUBECONFIG")); v != "" {
		cfg.Kubeconfig = v
	}
	if v := strings.TrimSpace(getenv(Prefix + "APPS_DOMAIN")); v != "" {
		cfg.AppsDomain = strings.TrimSuffix(strings.ToLower(v), ".")
	}
	if v := strings.TrimSpace(getenv(Prefix + "REGISTRY")); v != "" {
		cfg.Registry = strings.TrimSuffix(v, "/")
	}
	if v := strings.TrimSpace(getenv(Prefix + "CA_DIR")); v != "" {
		cfg.CADir = v
	}
	if v := strings.TrimSpace(getenv(Prefix + "MDNS")); v != "" {
		cfg.MDNS = v != "off"
	}
	if v := strings.TrimSpace(getenv(Prefix + "DB_FILE")); v != "" {
		if v == "off" {
			cfg.DatabasesFile = ""
		} else {
			cfg.DatabasesFile = v
		}
	}
	if v := strings.TrimSpace(getenv(Prefix + "DB_BACKUP_DIR")); v != "" {
		cfg.BackupDir = v
	}

	return cfg, nil
}

// parseDays reads a duration. It also accepts days, for example "30d".
func parseDays(v string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(v, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(v)
}

// parseSize reads a size in bytes with an optional unit: K, M, G or T
// (powers of 1024). "0" is no limit.
func parseSize(v string) (int64, error) {
	units := map[string]int64{"": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}
	s := strings.ToUpper(strings.TrimSuffix(strings.TrimSuffix(strings.ToUpper(v), "B"), "I"))
	num := strings.TrimRight(s, "KMGT")
	mul, ok := units[s[len(num):]]
	n, err := strconv.ParseInt(num, 10, 64)
	if !ok || err != nil || n < 0 {
		return 0, fmt.Errorf("the value %q is not a size (for example 10G)", v)
	}
	return n * mul, nil
}

// parseImages reads "17=image,18=image".
func parseImages(v string) (map[int]string, error) {
	out := map[int]string{}
	for _, part := range strings.Split(v, ",") {
		k, img, ok := strings.Cut(strings.TrimSpace(part), "=")
		n, err := strconv.Atoi(strings.TrimSpace(k))
		if !ok || err != nil || n < 10 || strings.TrimSpace(img) == "" {
			return nil, fmt.Errorf("the value %q is not valid: use for example 18=docker.io/library/postgres:18.1-alpine", part)
		}
		out[n] = strings.TrimSpace(img)
	}
	return out, nil
}
