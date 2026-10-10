// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"cosm/internal/depot"
)

// shippedGuideSHAs are the sha256 hashes of every earlier version of the agent
// guide (docs/dev-workspace.md) — every text a cosm build could have written to
// $COSM_DEPOT/dev/CLAUDE.md. A guide on disk that matches one of these is
// untouched and outdated, so setup may replace it. A guide matching neither
// these nor the current text was either edited by the user or written by a
// *newer* cosm, and is left alone — which is what stops an older binary from
// rolling a newer guide back. When you change docs/dev-workspace.md, add the
// previous text's hash here (TestGuideHistoryIsCurrent fails until you do).
var shippedGuideSHAs = map[string]bool{
	"f2b15a143670f5424b8c5cd7e0b567a22a31fa819b3c0b3883ba08e10da5e3b2": true, // v0.1.6
	"147d62a4e5c39515eb23957b1e2a9c1016b0a6a02262a510adf8b2bf80162980": true,
	"1dc50dc3454803e8e06a44178be7629e90f38233a6e3e2bb5281dc806b0c08ad": true,
	"66e6b37f4f8dc88c00f5943fb114e631ea76787dfbff05e0bdfcddad1339a7e8": true,
	"9af909f57b15ac06295eac5d10c9d69859bab35c6021541e796694f4f0598cc9": true,
	"c45621184bea865150412298b46c4f8454f71ae889281b6c1b336a7fb8465f16": true,
	"ed1ffad6201ad87745dbe374934f282d9a75a1373de4cf30e59ab481d189d898": true,
	"ef5cf3ff223ee70dc69492417f6645aef87de0438961bfca114c7a45da163ba4": true,
	"fcca1fa664b0451bdb531dbf07e74f9e4c9d130ab360c35d621ee04073c42a44": true,
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// syncDevGuide keeps $COSM_DEPOT/dev/CLAUDE.md current without ever losing a
// user's edits or downgrading a newer guide: it writes the guide when absent and
// replaces it only when it is a known earlier version (or with force). Returns the
// line to print, if any.
func syncDevGuide(d depot.Depot, force bool) string {
	if DevWorkspaceGuide == "" {
		return ""
	}
	guide := filepath.Join(d.Dev(), "CLAUDE.md")
	write := func(verb string) string {
		if err := os.WriteFile(guide, []byte(DevWorkspaceGuide), 0o644); err != nil {
			return ""
		}
		forgetLegacyGuideSHA(d)
		return fmt.Sprintf("%s agent guide at %s\n", verb, guide)
	}

	cur, err := os.ReadFile(guide)
	if err != nil {
		return write("Wrote")
	}
	switch got := sha256Hex(cur); {
	case got == sha256Hex([]byte(DevWorkspaceGuide)):
		forgetLegacyGuideSHA(d)
		return "" // already current
	case force:
		return write("Refreshed")
	case shippedGuideSHAs[got]:
		return write("Updated")
	}
	return fmt.Sprintf("Note: %s differs from this cosm's agent guide and from every earlier one —\n"+
		"      it has your edits, or comes from a newer cosm — so it was kept.\n"+
		"      To replace it with this cosm's guide: cosm setup --force-guide\n", guide)
}

// forgetLegacyGuideSHA drops config.json's devGuideSha. cosm v0.1.4 and v0.1.5
// replaced the guide whenever it matched that recorded hash, even with an older
// text of their own; with the hash gone they leave a newer guide alone.
func forgetLegacyGuideSHA(d depot.Depot) {
	if cfg, err := d.LoadConfig(); err == nil && cfg.DevGuideSHA != "" {
		cfg.DevGuideSHA = ""
		_ = d.SaveConfig(cfg)
	}
}
