// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package ext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"cosm/internal/depot"
	"cosm/internal/errs"
)

// Runner discovers and invokes extensions (§9.2).
type Runner struct {
	d depot.Depot
}

func NewRunner(d depot.Depot) *Runner { return &Runner{d: d} }

// exePath resolves cosm-ext-<id>: depot install takes precedence over PATH.
func (r *Runner) exePath(id string) (string, error) {
	name := "cosm-ext-" + id
	cand := filepath.Join(r.d.Extensions(), id, name)
	if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
		return cand, nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%w: %s", errs.ErrExtNotFound, name)
}

func (r *Runner) call(id, verb string, req, resp any) error {
	exe, err := r.exePath(id)
	if err != nil {
		return err
	}
	var in []byte
	if req != nil {
		if in, err = json.Marshal(req); err != nil {
			return err
		}
	}
	cmd := exec.Command(exe, verb)
	cmd.Stdin = bytes.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extension '%s %s' failed: %v\n%s", id, verb, err, errb.String())
	}
	if resp != nil {
		if err := json.Unmarshal(out.Bytes(), resp); err != nil {
			return fmt.Errorf("%w: bad response from '%s %s': %v", errs.ErrExtProtocol, id, verb, err)
		}
	}
	return nil
}

func (r *Runner) Info(id string) (Info, error) {
	var info Info
	if err := r.call(id, "info", nil, &info); err != nil {
		return Info{}, err
	}
	if info.Protocol != Protocol {
		return Info{}, fmt.Errorf("%w: extension %s speaks protocol %d, want %d", errs.ErrExtProtocol, id, info.Protocol, Protocol)
	}
	return info, nil
}

func (r *Runner) Build(id string, req BuildRequest) (BuildResponse, error) {
	var resp BuildResponse
	err := r.call(id, "build", req, &resp)
	return resp, err
}

func (r *Runner) Activate(id string, req ActivateRequest) (ActivateResponse, error) {
	var resp ActivateResponse
	err := r.call(id, "activate", req, &resp)
	return resp, err
}

func (r *Runner) Scaffold(id string, req ScaffoldRequest) (ScaffoldResponse, error) {
	var resp ScaffoldResponse
	err := r.call(id, "scaffold", req, &resp)
	return resp, err
}

func (r *Runner) Test(id string, req TestRequest) (TestResponse, error) {
	var resp TestResponse
	err := r.call(id, "test", req, &resp)
	return resp, err
}
