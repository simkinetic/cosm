// Package depot owns the on-disk depot layout (§5), its config (§11.3),
// bootstrap/resolution (§11.2), and the cross-process lock (§10.3).
package depot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"cosm/internal/semver"
	"cosm/internal/types"
)

// Config is $COSM_DEPOT/config.json (§11.3).
type Config struct {
	SchemaVersion int    `json:"schemaVersion"`
	Depot         string `json:"depot"`
	DefaultShell  string `json:"defaultShell,omitempty"`
	TemplatesRepo string `json:"templatesRepo,omitempty"`
}

// Depot is a handle to a depot root directory.
type Depot struct {
	Root string
}

func New(root string) Depot { return Depot{Root: root} }

// Path helpers (§5).
func (d Depot) Registries() string      { return filepath.Join(d.Root, "registries") }
func (d Depot) RegistriesFile() string  { return filepath.Join(d.Registries(), "registries.json") }
func (d Depot) Registry(name string) string {
	return filepath.Join(d.Registries(), name)
}
func (d Depot) Mirror(uuid string) string { return filepath.Join(d.Root, "mirrors", uuid+".git") }
func (d Depot) Source(uuid, commit string) string {
	return filepath.Join(d.Root, "sources", uuid, commit)
}
func (d Depot) Build(uuid, commit, key string) string {
	return filepath.Join(d.Root, "builds", uuid, commit, sanitizeKey(key))
}
func (d Depot) BuildlistCache(uuid, commit string) string {
	return filepath.Join(d.Root, "cache", "buildlists", uuid, commit+".json")
}
func (d Depot) Extensions() string     { return filepath.Join(d.Root, "extensions") }
func (d Depot) Dev() string            { return filepath.Join(d.Root, "dev") }
func (d Depot) WorkspaceFile() string  { return filepath.Join(d.Dev(), "workspace.json") }
func (d Depot) DevUnit(name string, major int) string {
	return filepath.Join(d.Dev(), fmt.Sprintf("%s@v%d", name, major))
}
func (d Depot) Logs() string       { return filepath.Join(d.Root, "logs") }
func (d Depot) ConfigFile() string { return filepath.Join(d.Root, "config.json") }

// sanitizeKey turns "sha256:abc" into a filesystem-safe segment.
func sanitizeKey(key string) string {
	out := make([]rune, 0, len(key))
	for _, r := range key {
		if r == ':' || r == '/' || r == '\\' {
			r = '_'
		}
		out = append(out, r)
	}
	return string(out)
}

var requiredDirs = []string{"registries", "mirrors", "sources", "builds", "cache", "extensions", "dev", "logs"}

// IsInitialized reports whether the depot structure exists.
func (d Depot) IsInitialized() bool {
	for _, sub := range requiredDirs {
		if _, err := os.Stat(filepath.Join(d.Root, sub)); err != nil {
			return false
		}
	}
	_, err := os.Stat(d.RegistriesFile())
	return err == nil
}

// Setup creates the depot structure idempotently and writes config.json (§11.1).
func (d Depot) Setup() error {
	for _, sub := range requiredDirs {
		if err := os.MkdirAll(filepath.Join(d.Root, sub), 0o755); err != nil {
			return err
		}
	}
	if _, err := os.Stat(d.RegistriesFile()); os.IsNotExist(err) {
		if err := os.WriteFile(d.RegistriesFile(), []byte("[]\n"), 0o644); err != nil {
			return err
		}
	}
	if _, err := os.Stat(d.ConfigFile()); os.IsNotExist(err) {
		cfg := Config{SchemaVersion: types.SchemaVersion, Depot: d.Root, DefaultShell: "bash"}
		if err := writeJSON(d.ConfigFile(), cfg); err != nil {
			return err
		}
	}
	return writeXDGPointer(d.Root)
}

// Lock acquires an exclusive advisory lock on the depot, blocking until
// available. The returned func releases it. Serializes concurrent processes.
func (d Depot) Lock() (func() error, error) {
	if err := os.MkdirAll(d.Root, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(d.Root, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() error {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return f.Close()
	}, nil
}

// ResolveRoot determines the depot root (§11.2): flag > $COSM_DEPOT > XDG
// config pointer > ~/.cosm.
func ResolveRoot(flag string) (string, error) {
	if flag != "" {
		return filepath.Abs(flag)
	}
	if e := os.Getenv("COSM_DEPOT"); e != "" {
		return filepath.Abs(e)
	}
	if p := xdgConfigPath(); p != "" {
		if data, err := os.ReadFile(p); err == nil {
			var cfg Config
			if json.Unmarshal(data, &cfg) == nil && cfg.Depot != "" {
				return cfg.Depot, nil
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cosm"), nil
}

func xdgConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "cosm", "config.json")
}

func writeXDGPointer(root string) error {
	p := xdgConfigPath()
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return writeJSON(p, Config{SchemaVersion: types.SchemaVersion, Depot: root})
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// UnitDirName returns the "<name>@v<major>" directory name for a dev checkout.
func UnitDirName(name string, major int) string { return fmt.Sprintf("%s@v%d", name, major) }

// MajorFromVersion is a convenience wrapper used by depot callers.
func MajorFromVersion(v string) (int, error) { return semver.Major(v) }
