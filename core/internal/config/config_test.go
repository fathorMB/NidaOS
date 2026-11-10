package config

import (
	"log/slog"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(env(nil))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Addr != DefaultAddr {
		t.Errorf("Addr = %q, want %q", cfg.Addr, DefaultAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != DefaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, DefaultShutdownTimeout)
	}
	if cfg.Root != "/" || cfg.PlanFile != DefaultPlanFile {
		t.Errorf("Root, PlanFile = %q, %q", cfg.Root, cfg.PlanFile)
	}
}

func TestLoadPaths(t *testing.T) {
	cfg, err := LoadFrom(env(map[string]string{
		"NIDA_CORE_ROOT":             "/tmp/host",
		"NIDA_CORE_PLAN_FILE":        "/plan.txt",
		"NIDA_CORE_POD_LOG_DIR":      "/pods",
		"NIDA_CORE_METRICS_INTERVAL": "1s",
	}))
	if err != nil || cfg.Root != "/tmp/host" || cfg.PlanFile != "/plan.txt" ||
		cfg.PodLogDir != "/pods" || cfg.MetricsInterval != time.Second {
		t.Errorf("cfg = %+v, err = %v", cfg, err)
	}

	cfg, err = LoadFrom(env(map[string]string{
		"NIDA_CORE_JOURNALCTL":       "sudo journalctl",
		"NIDA_CORE_LOG_BUFFER_LINES": "100",
	}))
	if err != nil || len(cfg.Journalctl) != 2 || cfg.Journalctl[1] != "journalctl" || cfg.LogBufferLines != 100 {
		t.Errorf("cfg = %+v, err = %v", cfg, err)
	}

	cfg, err = LoadFrom(env(map[string]string{
		"NIDA_CORE_LOG_DIR":       "/var/nida/standard/logs",
		"NIDA_CORE_LOG_RETENTION": "7d",
		"NIDA_CORE_LOG_MAX_SIZE":  "512M",
	}))
	if err != nil || cfg.LogDir != "/var/nida/standard/logs" || cfg.LogRetention != 7*24*time.Hour || cfg.LogMaxBytes != 512<<20 {
		t.Errorf("cfg = %+v, err = %v", cfg, err)
	}
	for v, want := range map[string]int64{"0": 0, "100": 100, "2K": 2048, "10G": 10 << 30, "1TiB": 1 << 40, "3gb": 3 << 30} {
		if n, err := parseSize(v); err != nil || n != want {
			t.Errorf("parseSize(%q) = %d, %v", v, n, err)
		}
	}

	cfg, err = LoadFrom(env(map[string]string{"NIDA_CORE_JOURNALCTL": "off", "NIDA_CORE_LOG_DIR": "off"}))
	if err != nil || cfg.Journalctl != nil || cfg.LogDir != "" {
		t.Errorf("cfg = %+v, err = %v", cfg, err)
	}
}

func TestLoadValues(t *testing.T) {
	cfg, err := LoadFrom(env(map[string]string{
		"NIDA_CORE_ADDR":             ":9000",
		"NIDA_CORE_LOG_LEVEL":        "debug",
		"NIDA_CORE_SHUTDOWN_TIMEOUT": "3s",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Addr != ":9000" || cfg.LogLevel != slog.LevelDebug || cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadBadValues(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"level":    {"NIDA_CORE_LOG_LEVEL": "loud"},
		"timeout":  {"NIDA_CORE_SHUTDOWN_TIMEOUT": "soon"},
		"negative": {"NIDA_CORE_SHUTDOWN_TIMEOUT": "-1s"},
		"fast":     {"NIDA_CORE_METRICS_INTERVAL": "10ms"},
		"interval": {"NIDA_CORE_METRICS_INTERVAL": "often"},
		"buffer":   {"NIDA_CORE_LOG_BUFFER_LINES": "10"},
		"keep":     {"NIDA_CORE_LOG_RETENTION": "soon"},
		"short":    {"NIDA_CORE_LOG_RETENTION": "10m"},
		"size":     {"NIDA_CORE_LOG_MAX_SIZE": "big"},
		"unit":     {"NIDA_CORE_LOG_MAX_SIZE": "10X"},
	} {
		if _, err := LoadFrom(env(m)); err == nil {
			t.Errorf("%s: LoadFrom gave no error", name)
		}
	}
}
