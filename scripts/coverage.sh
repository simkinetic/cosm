#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 Simkinetic
#
# SPDX-License-Identifier: MIT

# Runs the test suite with cross-package coverage and enforces per-package
# minimums on the algorithmic core (§16). CLI glue, main, and the extension
# binaries are covered behaviorally by the in-process/e2e tests.
set -euo pipefail

PROFILE="${PROFILE:-coverage.out}"
go test -coverpkg=./internal/...,./ -coverprofile="$PROFILE" ./... >/dev/null

go tool cover -func="$PROFILE" | python3 -c '
import sys, collections

MIN = {
    "resolve": 90, "buildkey": 90, "semver": 90, "errs": 85, "artifact": 80,
    "materialize": 78, "registry": 78, "service": 78, "develop": 80, "depot": 80,
}

tot = collections.defaultdict(lambda: [0.0, 0])
total_line = ""
for line in sys.stdin:
    p = line.split()
    if len(p) < 3:
        continue
    if p[0] == "total:":
        total_line = line.strip()
        continue
    pct = float(p[-1].rstrip("%"))
    pkg = p[0].rsplit("/", 1)[0].replace("cosm/internal/", "").replace("cosm/", "")
    tot[pkg][0] += pct
    tot[pkg][1] += 1

print("== total ==")
print(total_line)
print("== per package ==")
fail = False
for k in sorted(tot):
    s, n = tot[k]
    cov = s / n
    flag = ""
    if k in MIN and cov < MIN[k]:
        flag = "  FAIL < %d%%" % MIN[k]
        fail = True
    print("  %-14s %5.1f%%%s" % (k, cov, flag))

if fail:
    print("coverage gate FAILED")
    sys.exit(1)
print("coverage gate passed")
'
