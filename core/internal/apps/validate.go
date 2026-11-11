package apps

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Limits of the data of an app.
const (
	MaxNameLength   = 32
	MaxCopies       = 10
	MaxFolders      = 10
	MaxVariables    = 100
	MaxValueBytes   = 32 << 10
	MaxSecretBytes  = 64 << 10
	MinFolderBytes  = 1 << 20
	MinMemoryBytes  = 16 << 20
	MaxCheckSeconds = 300
)

var (
	nameRe       = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)
	folderNameRe = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)
	envNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	repoRe       = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	branchRe     = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	imageRe      = regexp.MustCompile(`^[a-z0-9.:-]+(/[a-z0-9._-]+)+(:[A-Za-z0-9._-]+)?(@sha256:[a-f0-9]{64})?$`)
)

// FieldError tells which field of a request is not valid.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

func fieldErr(field, format string, args ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// IsFieldError tells if err is a FieldError.
func IsFieldError(err error) bool {
	var fe *FieldError
	return errors.As(err, &fe)
}

// ValidateName examines the name of an app. The name is a DNS label: it is
// part of the address "<name>.nidaos.local" and of the namespace.
func ValidateName(name string) error {
	if len(name) == 0 || len(name) > MaxNameLength || !nameRe.MatchString(name) {
		return fieldErr("name", "use 1 to %d characters: lowercase letters, digits and '-'. Start with a letter, and do not end with '-'", MaxNameLength)
	}
	return nil
}

// ValidateImage examines an image reference.
func ValidateImage(ref string) error {
	if !imageRe.MatchString(ref) {
		return fieldErr("image", "use an image reference, for example localhost:5000/blog:1.2")
	}
	return nil
}

func validateSource(repo, branch string) error {
	if repo != "" && !repoRe.MatchString(repo) {
		return fieldErr("repository", "use the form owner/name of GitHub")
	}
	if branch != "" && (!branchRe.MatchString(branch) || strings.Contains(branch, "..") || len(branch) > 200) {
		return fieldErr("branch", "the branch name is not valid")
	}
	return nil
}

// ValidateConfig examines the config of a version.
func ValidateConfig(c Config) error {
	if c.Port != nil && (*c.Port < 1 || *c.Port > 65535) {
		return fieldErr("port", "use a number from 1 to 65535")
	}
	if hc := c.HealthCheck; hc != nil {
		if c.Port == nil {
			return fieldErr("health_check", "an app without a port cannot have a health check")
		}
		if !strings.HasPrefix(hc.Path, "/") || strings.ContainsAny(hc.Path, " \t\r\n") || len(hc.Path) > 200 {
			return fieldErr("health_check.path", "use an HTTP path that starts with '/', for example /healthz")
		}
		if hc.IntervalSeconds < 0 || hc.IntervalSeconds > MaxCheckSeconds {
			return fieldErr("health_check.interval_seconds", "use a number from 1 to %d, or 0 for the default", MaxCheckSeconds)
		}
		if hc.TimeoutSeconds < 0 || hc.TimeoutSeconds > 60 {
			return fieldErr("health_check.timeout_seconds", "use a number from 1 to 60, or 0 for the default")
		}
	}
	if len(c.Variables) > MaxVariables {
		return fieldErr("variables", "an app can have %d variables at most", MaxVariables)
	}
	for k, v := range c.Variables {
		if err := validateEnvName("variables", k); err != nil {
			return err
		}
		if len(v) > MaxValueBytes {
			return fieldErr("variables."+k, "the value is too long: %d bytes at most", MaxValueBytes)
		}
	}
	if c.Limits.CPUMillis < 0 || c.Limits.CPUMillis > 64000 {
		return fieldErr("limits.cpu_millis", "use a number from 1 to 64000, or 0 for no limit")
	}
	if c.Limits.MemoryBytes < 0 || (c.Limits.MemoryBytes > 0 && c.Limits.MemoryBytes < MinMemoryBytes) {
		return fieldErr("limits.memory_bytes", "use %d bytes or more, or 0 for no limit", MinMemoryBytes)
	}
	return nil
}

func validateEnvName(field, k string) error {
	if !envNameRe.MatchString(k) || len(k) > 100 {
		return fieldErr(field, "the name %q is not valid: use letters, digits and '_', and do not start with a digit", k)
	}
	if ReservedEnvName(k) {
		return fieldErr(field, "the name %q is for NidaOS: names that start with NIDA_ and the name PORT are reserved", k)
	}
	return nil
}

// ValidateSecret examines the name and the value of a secret.
func ValidateSecret(key, value string) error {
	if err := validateEnvName("name", key); err != nil {
		return err
	}
	if len(value) > MaxSecretBytes {
		return fieldErr("value", "the value is too long: %d bytes at most", MaxSecretBytes)
	}
	return nil
}

// ValidateTier examines the name of a storage tier.
func ValidateTier(field, tier string) error {
	if tier != TierPerformance && tier != TierStandard {
		return fieldErr(field, "use performance or standard")
	}
	return nil
}

func validateFolders(folders []Folder, copies int) error {
	if len(folders) > MaxFolders {
		return fieldErr("folders", "an app can have %d folders at most", MaxFolders)
	}
	if len(folders) > 0 && copies > 1 {
		return fieldErr("copies", "an app with folders has 1 copy at most, because two copies cannot write in the same folder")
	}
	names, paths := map[string]bool{}, map[string]bool{}
	for _, f := range folders {
		if len(f.Name) > 20 || !folderNameRe.MatchString(f.Name) {
			return fieldErr("folders.name", "the name %q is not valid: use 1 to 20 lowercase letters, digits and '-'", f.Name)
		}
		if names[f.Name] {
			return fieldErr("folders.name", "two folders have the name %q", f.Name)
		}
		names[f.Name] = true
		if !path.IsAbs(f.Path) || path.Clean(f.Path) != f.Path || f.Path == "/" {
			return fieldErr("folders.path", "the path %q is not valid: use an absolute path, for example /var/lib/app", f.Path)
		}
		if paths[f.Path] {
			return fieldErr("folders.path", "two folders have the path %q", f.Path)
		}
		paths[f.Path] = true
		if err := ValidateTier("folders.tier", f.Tier); err != nil {
			return err
		}
		if f.SizeBytes < MinFolderBytes {
			return fieldErr("folders.size_bytes", "use %d bytes or more", MinFolderBytes)
		}
	}
	return nil
}

// validateApp examines all the data of an app.
func validateApp(a App) error {
	if err := ValidateName(a.Name); err != nil {
		return err
	}
	if err := validateSource(a.Repository, a.Branch); err != nil {
		return err
	}
	if err := ValidateConfig(a.Config); err != nil {
		return err
	}
	if a.Copies < 0 || a.Copies > MaxCopies {
		return fieldErr("copies", "use a number from 0 to %d", MaxCopies)
	}
	return validateFolders(a.Folders, a.Copies)
}
