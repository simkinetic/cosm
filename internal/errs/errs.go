// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

// Package errs defines cosm's typed error taxonomy and exit-code mapping (§10).
// Callers match with errors.Is/As — never by string.
package errs

import (
	"errors"
	"fmt"
)

// Sentinel errors (§10.1). Wrap with fmt.Errorf("...: %w", errs.ErrX) to add context.
var (
	ErrUsage              = errors.New("usage error")
	ErrNoProject          = errors.New("no cosm.json where required")
	ErrRegistryNotFound   = errors.New("registry not found")
	ErrPackageNotFound    = errors.New("package/version not found in any registry")
	ErrVersionExists      = errors.New("version already exists")
	ErrDirtyWorktree      = errors.New("uncommitted changes")
	ErrNotInSync          = errors.New("local behind origin")
	ErrTagMoved           = errors.New("tag commit changed vs recorded")
	ErrIntegrityMismatch  = errors.New("content hash mismatch")
	ErrResolutionConflict = errors.New("resolution conflict")
	ErrExtNotFound        = errors.New("extension not found")
	ErrExtProtocol        = errors.New("extension protocol/version mismatch")
	ErrNoBinary           = errors.New("no matching binary artifact and no source access")
	ErrNetwork            = errors.New("network error")
	ErrInternal           = errors.New("internal error")
)

// BuildFailedError carries structured context for a failed extension build (§10.1).
type BuildFailedError struct {
	Package string
	Phase   string
	LogPath string
	Err     error
}

func (e *BuildFailedError) Error() string {
	var msg string
	if e.Phase == "test" {
		msg = fmt.Sprintf("tests failed for %s", e.Package)
	} else {
		msg = fmt.Sprintf("build failed for %s during %s", e.Package, e.Phase)
	}
	if e.LogPath != "" {
		msg += " (log: " + e.LogPath + ")"
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *BuildFailedError) Unwrap() error { return e.Err }

// ExitCode maps an error to a stable process exit code (§10.2).
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUsage):
		return 2
	case errors.Is(err, ErrRegistryNotFound),
		errors.Is(err, ErrPackageNotFound),
		errors.Is(err, ErrExtNotFound):
		return 3
	case errors.Is(err, ErrNetwork):
		return 4
	case errors.Is(err, ErrIntegrityMismatch), errors.Is(err, ErrTagMoved):
		return 5
	case errors.Is(err, ErrInternal):
		return 70
	}
	var bf *BuildFailedError
	if errors.As(err, &bf) {
		return 6
	}
	return 1
}
