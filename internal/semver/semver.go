// Package semver wraps golang.org/x/mod/semver with cosm-specific helpers:
// full SemVer 2.0.0 with a leading "v" (§6.1) and compatibility-unit keys (§6.3).
package semver

import (
	"fmt"
	"strconv"
	"strings"

	xsem "golang.org/x/mod/semver"
)

// IsValid reports whether v is a valid v-prefixed semantic version (partials
// like "v1"/"v1.2" are valid here — they are accepted only as constraints).
func IsValid(v string) bool { return xsem.IsValid(v) }

// Compare returns -1, 0, or +1 comparing a and b by SemVer precedence.
func Compare(a, b string) int { return xsem.Compare(a, b) }

// Max returns the higher of two versions by precedence (a on tie).
func Max(a, b string) string {
	if xsem.Compare(a, b) >= 0 {
		return a
	}
	return b
}

// Major returns the numeric major version of a valid semver.
func Major(v string) (int, error) {
	if !xsem.IsValid(v) {
		return 0, fmt.Errorf("invalid version %q", v)
	}
	return strconv.Atoi(strings.TrimPrefix(xsem.Major(v), "v"))
}

// ValidateExact requires a full identity version vX.Y.Z (rejects partials such
// as v1 or v1.2, which are only valid as constraints). Prerelease/build are OK.
func ValidateExact(v string) error {
	if !xsem.IsValid(v) {
		return fmt.Errorf("invalid semver %q (want vX.Y.Z)", v)
	}
	core := strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	if strings.Count(core, ".") != 2 {
		return fmt.Errorf("version %q must be vX.Y.Z (three components)", v)
	}
	return nil
}

// Version is a parsed core semantic version (prerelease/build dropped).
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Parse parses an exact vX.Y.Z into its numeric components.
func Parse(v string) (Version, error) {
	if err := ValidateExact(v); err != nil {
		return Version{}, err
	}
	core := strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	maj, _ := strconv.Atoi(parts[0])
	min, _ := strconv.Atoi(parts[1])
	pat, _ := strconv.Atoi(parts[2])
	return Version{Major: maj, Minor: min, Patch: pat}, nil
}

// UnitKey builds the compatibility-unit key "<uuid>@v<major>" (§6.3).
func UnitKey(uuid string, major int) string {
	return fmt.Sprintf("%s@v%d", uuid, major)
}

// SplitUnitKey parses "<uuid>@v<major>" back into its parts.
func SplitUnitKey(key string) (uuid string, major int, err error) {
	i := strings.LastIndex(key, "@")
	if i < 0 {
		return "", 0, fmt.Errorf("invalid unit key %q: missing '@'", key)
	}
	uuid = key[:i]
	if uuid == "" {
		return "", 0, fmt.Errorf("invalid unit key %q: empty uuid", key)
	}
	mpart := key[i+1:]
	if !strings.HasPrefix(mpart, "v") {
		return "", 0, fmt.Errorf("invalid unit key %q: major must start with 'v'", key)
	}
	major, err = strconv.Atoi(strings.TrimPrefix(mpart, "v"))
	if err != nil {
		return "", 0, fmt.Errorf("invalid unit key %q: bad major", key)
	}
	return uuid, major, nil
}
